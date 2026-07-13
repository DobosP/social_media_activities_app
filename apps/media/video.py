"""Video validation and transcoding for private-thread video attachments (ADR-0026).

Everything here treats the input as ATTACKER-CONTROLLED and runs ffprobe/ffmpeg inside the
same containment: ``-nostdin``, a protocol whitelist of ``file`` only (a crafted playlist can
otherwise make ffmpeg fetch remote URLs — SSRF), a hard wall-clock timeout with the whole
process group killed on expiry, and RLIMIT_CPU / RLIMIT_AS on the child as defence-in-depth
under the container's own cgroup caps. Validation happens BEFORE transcoding: container and
codec whitelists (most historical ffmpeg CVEs live in exotic demuxers/decoders we never
intend to accept), stream-count sanity, duration/dimension caps (decode-bomb class), and a
pixel-format allow-list.

The transcode re-encodes to one progressive H.264/AAC MP4 (``+faststart``) — the re-encode IS
the metadata strip (``-map_metadata -1 -map_chapters -1`` on top): GPS/device/creation tags
cannot survive it, and ffmpeg's default autorotate bakes any display-matrix rotation into the
pixels. Like processing.py, this module is deliberately settings-free: callers pass the knobs.
"""

import json
import os
import resource
import shutil
import signal
import subprocess
from dataclasses import dataclass

FFMPEG = "ffmpeg"
FFPROBE = "ffprobe"

# Container formats we accept, as reported by ffprobe format_name (comma-joined family names).
_ALLOWED_FORMATS = {"mov,mp4,m4a,3gp,3g2,mj2", "matroska,webm"}
# Decoders we are willing to run at all — everything a 2020s phone/browser produces.
_ALLOWED_VIDEO_CODECS = {"h264", "hevc", "vp8", "vp9", "av1", "mpeg4"}
_ALLOWED_AUDIO_CODECS = {"aac", "mp3", "opus", "vorbis"}
# Pixel-format families phones/browsers actually produce; exotic/high-bit planar formats are
# both attack surface and unpredictable resource use.
_ALLOWED_PIX_FMT_PREFIXES = ("yuv", "yuvj", "nv12", "nv21", "gray")

# MP4/MOV magic: bytes 4:8 == "ftyp". Matroska/WebM: EBML header.
_FTYP = b"ftyp"
_EBML = b"\x1a\x45\xdf\xa3"

# Child rlimits (defence-in-depth; the primary bound is the wall-clock timeout + container
# cgroups). Address space generous enough for a 4K decode buffer, far below box RAM.
_RLIMIT_AS_BYTES = 1 << 31  # 2 GiB


class VideoError(ValueError):
    """Upload is not a valid/allowed video, exceeds a cap, or could not be processed."""


def ffmpeg_available() -> bool:
    return bool(shutil.which(FFMPEG) and shutil.which(FFPROBE))


def looks_like_video(head: bytes) -> bool:
    """Cheap magic sniff on the first bytes of an upload — routing only (never a security
    boundary; ffprobe + the whitelists above are the real gate)."""
    return (len(head) >= 12 and head[4:8] == _FTYP) or head[:4] == _EBML


@dataclass
class ProbeResult:
    duration: float
    width: int
    height: int
    video_codec: str
    audio_codec: str | None


def _limit_child(cpu_seconds: int):
    # setrlimit only: the new session/process group comes from start_new_session=True (the
    # async-signal-safe C path), because this may be spawned from a daemon thread of the
    # multithreaded ASGI process, where running MORE Python between fork and exec than
    # strictly necessary risks the documented preexec_fn deadlock.
    def _apply():
        resource.setrlimit(resource.RLIMIT_CPU, (cpu_seconds, cpu_seconds))
        resource.setrlimit(resource.RLIMIT_AS, (_RLIMIT_AS_BYTES, _RLIMIT_AS_BYTES))

    return _apply


def _run(cmd: list[str], *, timeout: int) -> bytes:
    """Run ffmpeg/ffprobe against untrusted input. Returns stdout. Raises VideoError on any
    failure — non-zero exit, timeout (process group killed), or missing binary."""
    try:
        proc = subprocess.Popen(  # noqa: S603 — fixed argv, no shell, input path is ours
            cmd,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            start_new_session=True,  # own process group, so a timeout kills ffmpeg + children
            preexec_fn=_limit_child(timeout),  # noqa: PLW1509 — rlimits only, see above
        )
    except OSError as exc:
        raise VideoError("Video processing is not available on this host.") from exc
    try:
        stdout, stderr = proc.communicate(timeout=timeout)
    except subprocess.TimeoutExpired as exc:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except OSError:
            pass
        proc.wait()
        raise VideoError("Video processing timed out.") from exc
    if proc.returncode != 0:
        detail = stderr.decode(errors="replace")[-500:]
        raise VideoError(f"Video processing failed: {detail}")
    return stdout


def probe(path: str, *, timeout: int = 60) -> dict:
    """ffprobe the file (bounded analysis) and return the parsed JSON."""
    out = _run(
        [
            FFPROBE,
            "-v",
            "error",
            "-protocol_whitelist",
            "file",
            "-analyzeduration",
            "10M",
            "-probesize",
            "25M",
            "-print_format",
            "json",
            "-show_format",
            "-show_streams",
            path,
        ],
        timeout=timeout,
    )
    try:
        return json.loads(out.decode("utf-8", errors="replace"))
    except json.JSONDecodeError as exc:
        raise VideoError("Could not read the video file.") from exc


def validate_probe(
    info: dict,
    *,
    max_duration: float,
    max_side: int,
) -> ProbeResult:
    """Enforce the admission policy on ffprobe output. Raises VideoError with a user-safe
    message on every rejection path."""
    fmt = info.get("format") or {}
    if fmt.get("format_name") not in _ALLOWED_FORMATS:
        raise VideoError("Only MP4/MOV or WebM videos can be shared.")

    streams = info.get("streams") or []
    videos = [
        s
        for s in streams
        if s.get("codec_type") == "video"
        # An embedded cover-art image is technically a video stream; ignore it.
        and not (s.get("disposition") or {}).get("attached_pic")
    ]
    audios = [s for s in streams if s.get("codec_type") == "audio"]
    if len(videos) != 1:
        raise VideoError("The video must contain exactly one video track.")
    if len(audios) > 1:
        raise VideoError("The video must contain at most one audio track.")

    v = videos[0]
    if v.get("codec_name") not in _ALLOWED_VIDEO_CODECS:
        raise VideoError("This video uses an unsupported codec.")
    audio_codec = None
    if audios:
        audio_codec = audios[0].get("codec_name")
        if audio_codec not in _ALLOWED_AUDIO_CODECS:
            raise VideoError("This video uses an unsupported audio codec.")

    pix_fmt = v.get("pix_fmt") or ""
    if not pix_fmt.startswith(_ALLOWED_PIX_FMT_PREFIXES):
        raise VideoError("This video uses an unsupported pixel format.")

    try:
        width = int(v.get("width") or 0)
        height = int(v.get("height") or 0)
    except (TypeError, ValueError) as exc:
        raise VideoError("Could not read the video dimensions.") from exc
    if width <= 0 or height <= 0 or width > max_side or height > max_side:
        raise VideoError("The video resolution is not supported.")

    # Prefer the container duration; fall back to the stream's. Reject the absent/zero case —
    # a "duration-less" stream is exactly how a decode bomb hides its true cost.
    raw = fmt.get("duration") or v.get("duration")
    try:
        duration = float(raw)
    except (TypeError, ValueError) as exc:
        raise VideoError("Could not read the video duration.") from exc
    if duration <= 0:
        raise VideoError("Could not read the video duration.")
    if duration > max_duration:
        raise VideoError(f"Videos can be at most {int(max_duration)} seconds long.")

    return ProbeResult(
        duration=duration,
        width=width,
        height=height,
        video_codec=v.get("codec_name") or "",
        audio_codec=audio_codec,
    )


def transcode(
    src_path: str,
    dst_path: str,
    *,
    max_side: int,
    max_duration: float,
    crf: int,
    preset: str,
    audio_bitrate: str,
    threads: int,
    timeout: int,
) -> None:
    """One-shot transcode to a universal progressive MP4: H.264 High@4.1 + AAC, capped to
    ``max_side`` (never upscaled), metadata/chapters dropped, rotation baked (autorotate),
    ``+faststart`` so playback/seeking starts before full download."""
    scale = (
        f"scale=min({max_side}\\,iw):min({max_side}\\,ih):"
        "force_original_aspect_ratio=decrease:force_divisible_by=2"
    )
    _run(
        [
            FFMPEG,
            "-y",
            "-nostdin",
            "-protocol_whitelist",
            "file",
            "-i",
            src_path,
            "-map",
            "0:v:0",
            "-map",
            "0:a:0?",
            "-map_metadata",
            "-1",
            "-map_chapters",
            "-1",
            "-vf",
            f"{scale},format=yuv420p",
            "-c:v",
            "libx264",
            "-profile:v",
            "high",
            "-level",
            "4.1",
            "-preset",
            preset,
            "-crf",
            str(crf),
            "-c:a",
            "aac",
            "-b:a",
            audio_bitrate,
            "-ac",
            "2",
            "-metadata:s:v:0",
            "rotate=0",
            "-t",
            str(max_duration),
            "-threads",
            str(threads),
            "-movflags",
            "+faststart",
            "-f",
            "mp4",
            dst_path,
        ],
        timeout=timeout,
    )


def extract_poster(video_path: str, *, duration: float, timeout: int = 60) -> bytes:
    """One JPEG frame from the ALREADY-TRANSCODED output (so it inherits the strip/rotation),
    handed to the ordinary image pipeline by the caller for the canonical re-encode.

    ``-threads 1`` is load-bearing, not an optimisation: without it ffmpeg sizes the mjpeg
    frame-thread encoder pool from the host's core count, and on a many-core box those
    per-thread allocations overflow the sandbox's RLIMIT_AS — a single still frame needs one
    thread."""
    seek = "1" if duration >= 2 else "0"
    return _run(
        [
            FFMPEG,
            "-nostdin",
            "-protocol_whitelist",
            "file",
            "-ss",
            seek,
            "-i",
            video_path,
            "-frames:v",
            "1",
            "-q:v",
            "3",
            "-threads",
            "1",
            "-f",
            "image2pipe",
            "-vcodec",
            "mjpeg",
            "-",
        ],
        timeout=timeout,
    )


def sample_frames(
    video_path: str,
    scratch_dir: str,
    *,
    interval_seconds: int,
    max_frames: int,
    timeout: int = 120,
) -> list[bytes]:
    """JPEG frames sampled every ``interval_seconds`` from the transcoded output, for the
    fail-closed frame scan (the perceptual blocklist matches known-bad imagery appearing
    inside a video — ADR-0026 §3 step 4)."""
    pattern = os.path.join(scratch_dir, "scan_%04d.jpg")
    # select (not fps): the first term picks frame 0 unconditionally, the second one frame per
    # interval after it. The fps filter centres its first sample at interval/2, so a clip
    # shorter than that produced ZERO frames and a spurious ffmpeg failure — a sub-3s clip
    # must still yield its frame-0 for the fail-closed scan. -threads 1 for the same
    # RLIMIT_AS reason as extract_poster.
    selector = f"select=isnan(prev_selected_t)+gte(t-prev_selected_t\\,{interval_seconds})"
    _run(
        [
            FFMPEG,
            "-nostdin",
            "-protocol_whitelist",
            "file",
            "-i",
            video_path,
            "-vf",
            selector,
            "-fps_mode",
            "vfr",
            "-frames:v",
            str(max_frames),
            "-q:v",
            "3",
            "-threads",
            "1",
            pattern,
        ],
        timeout=timeout,
    )
    frames = []
    for name in sorted(os.listdir(scratch_dir)):
        if name.startswith("scan_") and name.endswith(".jpg"):
            with open(os.path.join(scratch_dir, name), "rb") as fh:
                frames.append(fh.read())
    return frames
