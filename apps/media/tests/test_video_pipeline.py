"""Video validation + transcoding pipeline (ADR-0026): the ffprobe admission policy (container/
codec/stream-count/pixel-format/dimension/duration whitelists), the sandboxed ffmpeg transcode
(single progressive H.264/AAC MP4, metadata stripped, never upscaled, timeout-bounded), and the
poster/frame-sampling helpers used by the async processing pipeline. No DB needed anywhere here —
this module is deliberately settings-free (callers in apps/media/services.py pass the knobs)."""

import copy
import io
import json
import subprocess

import pytest
from PIL import Image

from apps.media.video import (
    ProbeResult,
    VideoError,
    _run,
    extract_poster,
    ffmpeg_available,
    looks_like_video,
    probe,
    sample_frames,
    transcode,
    validate_probe,
)

no_ffmpeg = pytest.mark.skipif(
    not ffmpeg_available(), reason="ffmpeg/ffprobe not installed on this host"
)


def _ffmpeg(*args, timeout=30):
    """Build a fixture video/asset with ffmpeg directly (not the module under test)."""
    result = subprocess.run(
        ["ffmpeg", "-y", "-nostdin", *args],
        capture_output=True,
        timeout=timeout,
    )
    assert result.returncode == 0, result.stderr.decode(errors="replace")[-2000:]


def _ffprobe_json(path, timeout=30):
    result = subprocess.run(
        [
            "ffprobe",
            "-v",
            "error",
            "-print_format",
            "json",
            "-show_format",
            "-show_streams",
            path,
        ],
        capture_output=True,
        timeout=timeout,
    )
    assert result.returncode == 0, result.stderr.decode(errors="replace")[-2000:]
    return json.loads(result.stdout.decode("utf-8"))


@pytest.fixture(scope="module")
def gps_video(tmp_path_factory):
    """A real, valid, GPS-tagged 320x240 h264/aac mp4 — the "happy path" source fixture."""
    d = tmp_path_factory.mktemp("gps_video")
    path = str(d / "src.mp4")
    _ffmpeg(
        "-f",
        "lavfi",
        "-i",
        "testsrc=duration=2:size=320x240:rate=10",
        "-f",
        "lavfi",
        "-i",
        "sine=frequency=440:duration=2",
        "-pix_fmt",
        "yuv420p",
        "-c:v",
        "libx264",
        "-c:a",
        "aac",
        "-metadata",
        "location=+44.4268+26.1025/",
        "-metadata",
        "com.apple.quicktime.location.ISO6709=+44.4268+26.1025/",
        path,
    )
    return path


@pytest.fixture(scope="module")
def transcoded_video(gps_video, tmp_path_factory):
    """The gps_video fixture run through the actual transcode() under test, shared read-only by
    several tests below (poster/frame-sampling/output-shape checks)."""
    d = tmp_path_factory.mktemp("transcoded")
    dst = str(d / "out.mp4")
    transcode(
        gps_video,
        dst,
        max_side=1280,
        max_duration=90,
        crf=23,
        preset="veryfast",
        audio_bitrate="96k",
        threads=1,
        timeout=60,
    )
    return dst


@pytest.fixture(scope="module")
def large_source_video(tmp_path_factory):
    """A 1920x1080 1s source — for the downscale-to-max_side test."""
    d = tmp_path_factory.mktemp("large_video")
    path = str(d / "large.mp4")
    _ffmpeg(
        "-f",
        "lavfi",
        "-i",
        "testsrc=duration=1:size=1920x1080:rate=10",
        "-pix_fmt",
        "yuv420p",
        "-c:v",
        "libx264",
        path,
    )
    return path


# --- looks_like_video (magic sniff) ---------------------------------------------------------


def test_looks_like_video_true_for_mp4_head():
    # bytes 4:8 == b"ftyp" is the MP4/MOV/3GP family signature.
    head = b"\x00\x00\x00\x18ftypmp42" + b"\x00" * 8
    assert looks_like_video(head) is True


def test_looks_like_video_true_for_webm_ebml_head():
    head = b"\x1a\x45\xdf\xa3" + b"junk-after-ebml-id-bytes"
    assert looks_like_video(head) is True


def test_looks_like_video_false_for_png_head():
    head = b"\x89PNG\r\n\x1a\n" + b"\x00" * 20
    assert looks_like_video(head) is False


def test_looks_like_video_false_for_jpeg_head():
    head = b"\xff\xd8\xff\xe0" + b"JFIF\x00" + b"\x00" * 12
    assert looks_like_video(head) is False


def test_looks_like_video_false_for_pdf_head():
    head = b"%PDF-1.4\n1 0 obj<<>>" + b"\x00" * 4
    assert looks_like_video(head) is False


def test_looks_like_video_false_for_short_buffer():
    # Too short to even contain bytes 4:8 and not the (4-byte) EBML signature either.
    assert looks_like_video(b"ftyp") is False
    assert looks_like_video(b"") is False


# --- validate_probe against synthetic ffprobe dicts (no ffmpeg needed) ----------------------


def _valid_probe_dict():
    """A minimal, fully-compliant ffprobe -show_format -show_streams JSON dict: one h264 video
    stream + one aac audio stream in an MP4-family container."""
    return {
        "format": {
            "format_name": "mov,mp4,m4a,3gp,3g2,mj2",
            "duration": "10.5",
        },
        "streams": [
            {
                "codec_type": "video",
                "codec_name": "h264",
                "width": 640,
                "height": 480,
                "pix_fmt": "yuv420p",
                "duration": "10.5",
                "disposition": {"attached_pic": 0},
            },
            {
                "codec_type": "audio",
                "codec_name": "aac",
                "duration": "10.5",
            },
        ],
    }


def test_validate_probe_ok_case_returns_correct_fields():
    result = validate_probe(_valid_probe_dict(), max_duration=90, max_side=1280)
    assert result == ProbeResult(
        duration=10.5, width=640, height=480, video_codec="h264", audio_codec="aac"
    )


def test_validate_probe_disallowed_container_rejected():
    info = _valid_probe_dict()
    info["format"]["format_name"] = "avi"
    with pytest.raises(VideoError, match="MP4/MOV or WebM"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_two_video_streams_rejected():
    info = _valid_probe_dict()
    extra = copy.deepcopy(info["streams"][0])
    info["streams"].append(extra)
    with pytest.raises(VideoError, match="exactly one video track"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_attached_pic_cover_is_ignored():
    # A second "video" stream that is actually an embedded cover-art image (attached_pic
    # disposition) must not count toward the video-stream-count check.
    info = _valid_probe_dict()
    cover = {
        "codec_type": "video",
        "codec_name": "mjpeg",
        "width": 200,
        "height": 200,
        "pix_fmt": "yuvj420p",
        "duration": "0",
        "disposition": {"attached_pic": 1},
    }
    info["streams"].append(cover)
    result = validate_probe(info, max_duration=90, max_side=1280)
    # The primary video stream's own fields win — the cover art is invisible to the result.
    assert result.video_codec == "h264"
    assert (result.width, result.height) == (640, 480)


def test_validate_probe_two_audio_streams_rejected():
    info = _valid_probe_dict()
    info["streams"].append(copy.deepcopy(info["streams"][1]))
    with pytest.raises(VideoError, match="at most one audio track"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_disallowed_video_codec_rejected():
    info = _valid_probe_dict()
    info["streams"][0]["codec_name"] = "prores"
    with pytest.raises(VideoError, match="unsupported codec"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_disallowed_audio_codec_rejected():
    info = _valid_probe_dict()
    info["streams"][1]["codec_name"] = "flac"
    with pytest.raises(VideoError, match="unsupported audio codec"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_disallowed_pix_fmt_rejected():
    info = _valid_probe_dict()
    info["streams"][0]["pix_fmt"] = "rgb48le"
    with pytest.raises(VideoError, match="unsupported pixel format"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_width_over_max_side_rejected():
    info = _valid_probe_dict()
    info["streams"][0]["width"] = 2000
    with pytest.raises(VideoError, match="resolution is not supported"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_height_over_max_side_rejected():
    info = _valid_probe_dict()
    info["streams"][0]["height"] = 2000
    with pytest.raises(VideoError, match="resolution is not supported"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_zero_duration_rejected():
    info = _valid_probe_dict()
    info["format"]["duration"] = "0"
    info["streams"][0]["duration"] = "0"
    with pytest.raises(VideoError, match="Could not read the video duration"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_missing_duration_key_rejected():
    # Neither the container nor the video stream reports a duration at all — the classic
    # "duration-less stream" shape a decode bomb uses to hide its true cost.
    info = _valid_probe_dict()
    del info["format"]["duration"]
    del info["streams"][0]["duration"]
    with pytest.raises(VideoError, match="Could not read the video duration"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_duration_falls_back_to_stream_duration():
    # Positive companion to the "missing" case above: when the container omits duration but the
    # video stream reports one, the stream value is used (not a rejection).
    info = _valid_probe_dict()
    del info["format"]["duration"]
    info["streams"][0]["duration"] = "7.25"
    result = validate_probe(info, max_duration=90, max_side=1280)
    assert result.duration == 7.25


def test_validate_probe_duration_over_max_rejected():
    info = _valid_probe_dict()
    info["format"]["duration"] = "200"
    with pytest.raises(VideoError, match=r"at most 90 seconds"):
        validate_probe(info, max_duration=90, max_side=1280)


def test_validate_probe_error_messages_are_user_safe():
    # Rejection messages must never leak stack/internal detail — they get shown to the uploader.
    info = _valid_probe_dict()
    info["format"]["format_name"] = "avi"
    with pytest.raises(VideoError) as excinfo:
        validate_probe(info, max_duration=90, max_side=1280)
    message = str(excinfo.value)
    assert "Traceback" not in message
    assert 'File "' not in message
    assert len(message) < 200


# --- probe() + validate_probe on a REAL fixture ----------------------------------------------


@no_ffmpeg
def test_real_probe_and_validate_on_fixture(gps_video):
    info = probe(gps_video)
    result = validate_probe(info, max_duration=90, max_side=1280)
    assert result.video_codec == "h264"
    assert result.audio_codec == "aac"
    assert (result.width, result.height) == (320, 240)
    assert 1.5 < result.duration < 3.0  # ~2s, allow for container rounding


@no_ffmpeg
def test_probe_then_validate_on_a_png_disguised_as_mp4_is_rejected(tmp_path):
    # ffprobe happily demuxes a lone image as a one-frame "video" stream (format_name
    # "png_pipe", codec "png") — probe() itself does NOT raise for this input. The real
    # rejection is validate_probe()'s container whitelist, exercised here end-to-end against a
    # REAL ffprobe result (not a synthetic dict) to prove the two layers compose correctly.
    fake = tmp_path / "not_really_a_video.mp4"
    png = Image.new("RGB", (4, 4), (1, 2, 3))
    buf = io.BytesIO()
    png.save(buf, format="PNG")
    fake.write_bytes(buf.getvalue())
    info = probe(str(fake))
    with pytest.raises(VideoError, match="MP4/MOV or WebM"):
        validate_probe(info, max_duration=90, max_side=1280)


@no_ffmpeg
def test_probe_on_random_bytes_raises_video_error(tmp_path):
    fake = tmp_path / "garbage.mp4"
    fake.write_bytes(b"not a video at all, just filler bytes" * 20)
    with pytest.raises(VideoError):
        probe(str(fake))


# --- transcode() ------------------------------------------------------------------------------


@no_ffmpeg
def test_transcode_output_is_h264_aac_mp4_yuv420p(transcoded_video):
    info = probe(transcoded_video)
    result = validate_probe(info, max_duration=90, max_side=1280)
    assert result.video_codec == "h264"
    assert result.audio_codec == "aac"
    streams = info["streams"]
    video_stream = next(s for s in streams if s["codec_type"] == "video")
    assert video_stream["pix_fmt"] == "yuv420p"
    assert info["format"]["format_name"] == "mov,mp4,m4a,3gp,3g2,mj2"


@no_ffmpeg
def test_transcode_does_not_upscale_a_small_source(transcoded_video):
    # Source is 320x240, well under max_side=1280 — the output must stay exactly that size,
    # never upscaled to fill the cap.
    info = probe(transcoded_video)
    video_stream = next(s for s in info["streams"] if s["codec_type"] == "video")
    assert (int(video_stream["width"]), int(video_stream["height"])) == (320, 240)


@no_ffmpeg
def test_transcode_plays_for_close_to_the_source_duration(transcoded_video):
    info = probe(transcoded_video)
    result = validate_probe(info, max_duration=90, max_side=1280)
    assert result.duration > 1.5


@no_ffmpeg
def test_transcode_strips_gps_location_metadata(transcoded_video):
    # The source fixture embeds two GPS tags (`location` + Apple ISO6709). The transcode's
    # `-map_metadata -1 -map_chapters -1` must strip them — this is the metadata-strip privacy
    # guarantee documented in ADR-0026, checked against real ffprobe tag output on the OUTPUT.
    info = _ffprobe_json(transcoded_video)
    format_tags = {k.lower(): v for k, v in (info.get("format", {}).get("tags") or {}).items()}
    for key, value in format_tags.items():
        assert "location" not in key, f"format tag {key!r}={value!r} leaked from source"
        assert "iso6709" not in key, f"format tag {key!r}={value!r} leaked from source"
    for stream in info.get("streams", []):
        stream_tags = {k.lower(): v for k, v in (stream.get("tags") or {}).items()}
        for key, value in stream_tags.items():
            assert "location" not in key, f"stream tag {key!r}={value!r} leaked from source"
            assert "iso6709" not in key, f"stream tag {key!r}={value!r} leaked from source"


@no_ffmpeg
def test_transcode_downscales_large_source_to_max_side(large_source_video, tmp_path):
    dst = str(tmp_path / "downscaled.mp4")
    transcode(
        large_source_video,
        dst,
        max_side=1280,
        max_duration=90,
        crf=28,
        preset="veryfast",
        audio_bitrate="96k",
        threads=1,
        timeout=60,
    )
    info = probe(dst)
    video_stream = next(s for s in info["streams"] if s["codec_type"] == "video")
    width, height = int(video_stream["width"]), int(video_stream["height"])
    assert max(width, height) == 1280
    assert width % 2 == 0 and height % 2 == 0  # force_divisible_by=2 in the scale filter


# --- extract_poster / sample_frames ------------------------------------------------------------


@no_ffmpeg
def test_extract_poster_returns_openable_jpeg(transcoded_video):
    data = extract_poster(transcoded_video, duration=2.0)
    assert data[:2] == b"\xff\xd8"  # JPEG SOI marker
    img = Image.open(io.BytesIO(data))
    img.load()
    assert img.format == "JPEG"


@no_ffmpeg
def test_sample_frames_returns_openable_jpegs(transcoded_video, tmp_path):
    scratch = tmp_path / "scan"
    scratch.mkdir()
    frames = sample_frames(
        transcoded_video, str(scratch), interval_seconds=1, max_frames=5, timeout=60
    )
    assert 1 <= len(frames) <= 5
    for frame in frames:
        assert frame[:2] == b"\xff\xd8"
        img = Image.open(io.BytesIO(frame))
        img.load()
        assert img.format == "JPEG"


# --- timeout kill path --------------------------------------------------------------------------


@no_ffmpeg
def test_run_kills_process_group_on_timeout():
    # `-re` paces the synthetic source at real-time speed, so the child spends most of its time
    # asleep (low CPU use) rather than CPU-bound — this keeps the wall-clock timeout the
    # deterministic bound here rather than racing the RLIMIT_CPU defence-in-depth cap, which is
    # set to the same second count (see _limit_child). Without the kill, this would run ~30s.
    with pytest.raises(VideoError, match="timed out"):
        _run(
            [
                "ffmpeg",
                "-nostdin",
                "-re",
                "-f",
                "lavfi",
                "-i",
                "testsrc=duration=30:size=640x480:rate=30",
                "-f",
                "null",
                "-",
            ],
            timeout=1,
        )


@no_ffmpeg
def test_sample_frames_yields_frame_zero_for_clips_shorter_than_interval(
    transcoded_video, tmp_path
):
    """Regression (review-caught): the fps filter centres its first sample at interval/2, so a
    clip shorter than that produced ZERO frames and a spurious ffmpeg failure — every sub-3s
    clip was finalised FAILED at the shipped 5s default. The select-based sampler must always
    emit frame 0 (the 2s fixture is shorter than the 5s interval used here)."""
    scratch = tmp_path / "short-scan"
    scratch.mkdir()
    frames = sample_frames(
        transcoded_video, str(scratch), interval_seconds=5, max_frames=25, timeout=60
    )
    assert len(frames) >= 1
    img = Image.open(io.BytesIO(frames[0]))
    img.load()
    assert img.format == "JPEG"
