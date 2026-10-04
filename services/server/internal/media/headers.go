package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/bits"
	"strconv"
)

type imageHeader struct {
	format                     string
	width, height, orientation int
}

// Header parsing rejects pixel bombs before a codec receives any source pixels.
func parseImageHeader(b []byte) (imageHeader, error) {
	h := imageHeader{orientation: 1}
	switch {
	case len(b) >= 24 && bytes.Equal(b[:8], []byte("\x89PNG\r\n\x1a\n")):
		if string(b[12:16]) != "IHDR" || binary.BigEndian.Uint32(b[8:12]) != 13 {
			return h, ErrRejected
		}
		h.format = "PNG"
		h.width = int(binary.BigEndian.Uint32(b[16:20]))
		h.height = int(binary.BigEndian.Uint32(b[20:24]))
		for pos := 8; pos+12 <= len(b); {
			n := int(binary.BigEndian.Uint32(b[pos : pos+4]))
			if n < 0 || n > len(b)-pos-12 {
				break
			}
			if string(b[pos+4:pos+8]) == "eXIf" {
				h.orientation = exifOrientation(b[pos+8 : pos+8+n])
			}
			pos += n + 12
		}
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		h.format = "WEBP"
		for pos := 12; pos+8 <= len(b); {
			n := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
			start := pos + 8
			if n < 0 || n > len(b)-start {
				return h, ErrRejected
			}
			chunk := b[start : start+n]
			switch string(b[pos : pos+4]) {
			case "VP8X":
				if len(chunk) < 10 {
					return h, ErrRejected
				}
				h.width = 1 + uint24(chunk[4:7])
				h.height = 1 + uint24(chunk[7:10])
			case "VP8 ":
				if len(chunk) < 10 || !bytes.Equal(chunk[3:6], []byte{0x9d, 0x01, 0x2a}) {
					return h, ErrRejected
				}
				h.width = int(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff)
				h.height = int(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff)
			case "VP8L":
				if len(chunk) < 5 || chunk[0] != 0x2f {
					return h, ErrRejected
				}
				x := binary.LittleEndian.Uint32(chunk[1:5])
				h.width = int(x&0x3fff) + 1
				h.height = int((x>>14)&0x3fff) + 1
			case "EXIF":
				if len(chunk) > 6 && string(chunk[:6]) == "Exif\x00\x00" {
					chunk = chunk[6:]
				}
				h.orientation = exifOrientation(chunk)
			}
			pos = start + n + (n % 2)
		}
	case len(b) >= 4 && b[0] == 0xff && b[1] == 0xd8:
		h.format = "JPEG"
		for pos := 2; pos+4 <= len(b); {
			if b[pos] != 0xff {
				return h, ErrRejected
			}
			for pos < len(b) && b[pos] == 0xff {
				pos++
			}
			if pos >= len(b) {
				break
			}
			marker := b[pos]
			pos++
			if marker == 0xd9 || marker == 0xda {
				break
			}
			if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
				continue
			}
			if pos+2 > len(b) {
				return h, ErrRejected
			}
			n := int(binary.BigEndian.Uint16(b[pos : pos+2]))
			if n < 2 || n > len(b)-pos {
				return h, ErrRejected
			}
			seg := b[pos+2 : pos+n]
			if marker == 0xe1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
				h.orientation = exifOrientation(seg[6:])
			}
			if (marker >= 0xc0 && marker <= 0xc3) || (marker >= 0xc5 && marker <= 0xc7) || (marker >= 0xc9 && marker <= 0xcb) || (marker >= 0xcd && marker <= 0xcf) {
				if len(seg) < 6 {
					return h, ErrRejected
				}
				h.height = int(binary.BigEndian.Uint16(seg[1:3]))
				h.width = int(binary.BigEndian.Uint16(seg[3:5]))
			}
			pos += n
		}
	case isAVIF(b):
		h.format = "AVIF"
		w, hgt, err := avifDimensions(b, 0)
		if err != nil {
			return h, err
		}
		h.width = w
		h.height = hgt
	default:
		return h, ErrRejected
	}
	if h.width <= 0 || h.height <= 0 {
		return h, ErrRejected
	}
	return h, nil
}
func isAVIF(b []byte) bool {
	if len(b) < 16 || string(b[4:8]) != "ftyp" {
		return false
	}
	n := int(binary.BigEndian.Uint32(b[:4]))
	if n < 16 || n > len(b) {
		return false
	}
	if string(b[8:12]) == "avif" || string(b[8:12]) == "avis" {
		return true
	}
	for pos := 16; pos+4 <= n; pos += 4 {
		if string(b[pos:pos+4]) == "avif" || string(b[pos:pos+4]) == "avis" {
			return true
		}
	}
	return false
}
func uint24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
func exifOrientation(b []byte) int {
	if len(b) < 8 {
		return 1
	}
	var order binary.ByteOrder
	if string(b[:2]) == "II" {
		order = binary.LittleEndian
	} else if string(b[:2]) == "MM" {
		order = binary.BigEndian
	} else {
		return 1
	}
	if order.Uint16(b[2:4]) != 42 {
		return 1
	}
	offset := int(order.Uint32(b[4:8]))
	if offset < 8 || offset+2 > len(b) {
		return 1
	}
	n := int(order.Uint16(b[offset : offset+2]))
	offset += 2
	if n > 512 || n > (len(b)-offset)/12 {
		return 1
	}
	for i := 0; i < n; i++ {
		p := b[offset+i*12 : offset+(i+1)*12]
		if order.Uint16(p[:2]) == 0x112 && order.Uint16(p[2:4]) == 3 && order.Uint32(p[4:8]) == 1 {
			v := int(order.Uint16(p[8:10]))
			if v >= 1 && v <= 8 {
				return v
			}
		}
	}
	return 1
}
func avifDimensions(b []byte, depth int) (int, int, error) {
	if depth > 16 {
		return 0, 0, ErrRejected
	}
	maxW, maxH := 0, 0
	for pos := 0; pos+8 <= len(b); {
		n64 := uint64(binary.BigEndian.Uint32(b[pos : pos+4]))
		head := 8
		if n64 == 1 {
			if pos+16 > len(b) {
				return 0, 0, ErrRejected
			}
			n64 = binary.BigEndian.Uint64(b[pos+8 : pos+16])
			head = 16
		} else if n64 == 0 {
			n64 = uint64(len(b) - pos)
		}
		if n64 < uint64(head) || n64 > uint64(len(b)-pos) {
			return 0, 0, ErrRejected
		}
		n := int(n64)
		typ := string(b[pos+4 : pos+8])
		body := b[pos+head : pos+n]
		if typ == "ispe" {
			if len(body) < 12 {
				return 0, 0, ErrRejected
			}
			maxW = max(maxW, int(binary.BigEndian.Uint32(body[4:8])))
			maxH = max(maxH, int(binary.BigEndian.Uint32(body[8:12])))
		}
		if typ == "meta" || typ == "iprp" || typ == "ipco" {
			if typ == "meta" {
				if len(body) < 4 {
					return 0, 0, ErrRejected
				}
				body = body[4:]
			}
			w, h, e := avifDimensions(body, depth+1)
			if e != nil {
				return 0, 0, e
			}
			maxW = max(maxW, w)
			maxH = max(maxH, h)
		}
		pos += n
	}
	return maxW, maxH, nil
}

// DHash converts bounded pixels to luminance and uses an antialiased Lanczos-3
// resize with 22-bit coefficients, matching the reference fingerprint pipeline.
// It is a duplicate heuristic, not an authoritative CSAM matcher.
func DHash(img image.Image) string {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 || w > 2048 || h > 2048 {
		return ""
	}
	gray := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			gray[y*w+x] = uint8((19595*uint32(c.R) + 38470*uint32(c.G) + 7471*uint32(c.B) + 32768) >> 16)
		}
	}
	gray = resizeGray(gray, w, h, 9, 8)
	var v uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			a := gray[y*9+x]
			c := gray[y*9+x+1]
			v <<= 1
			if a > c {
				v |= 1
			}
		}
	}
	if v == 0 || v == ^uint64(0) {
		return ""
	}
	return fmt.Sprintf("%016x", v)
}

type weights struct {
	first int
	coef  []int64
}

func lanczosWeights(in, out int) []weights {
	scale := float64(in) / float64(out)
	filterScale := math.Max(1, scale)
	support := 3 * filterScale
	result := make([]weights, out)
	sinc := func(x float64) float64 {
		if x == 0 {
			return 1
		}
		x *= math.Pi
		return math.Sin(x) / x
	}
	for i := 0; i < out; i++ {
		center := (float64(i) + 0.5) * scale
		lo := max(0, int(center-support+0.5))
		hi := min(in, int(center+support+0.5))
		values := make([]float64, hi-lo)
		sum := 0.0
		for j := range values {
			x := (float64(lo+j) - center + 0.5) / filterScale
			if x >= -3 && x < 3 {
				values[j] = sinc(x) * sinc(x/3)
			}
			sum += values[j]
		}
		result[i] = weights{first: lo, coef: make([]int64, len(values))}
		for j, x := range values {
			if sum != 0 {
				x /= sum
			}
			result[i].coef[j] = int64(math.Round(x * (1 << 22)))
		}
	}
	return result
}
func resizeGray(src []byte, w, h, tw, th int) []byte {
	clip := func(v int64) byte {
		v >>= 22
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return byte(v)
	}
	if w != tw {
		coeff := lanczosWeights(w, tw)
		out := make([]byte, tw*h)
		for y := 0; y < h; y++ {
			for x, c := range coeff {
				sum := int64(1 << 21)
				for i, k := range c.coef {
					sum += int64(src[y*w+c.first+i]) * k
				}
				out[y*tw+x] = clip(sum)
			}
		}
		src = out
		w = tw
	}
	if h != th {
		coeff := lanczosWeights(h, th)
		out := make([]byte, w*th)
		for y, c := range coeff {
			for x := 0; x < w; x++ {
				sum := int64(1 << 21)
				for i, k := range c.coef {
					sum += int64(src[(c.first+i)*w+x]) * k
				}
				out[y*w+x] = clip(sum)
			}
		}
		src = out
	}
	return src
}
func Hamming(a, b string) (int, error) {
	if len(a) != 16 || len(b) != 16 {
		return 0, errors.New("invalid perceptual hash")
	}
	x, e := strconv.ParseUint(a, 16, 64)
	if e != nil {
		return 0, e
	}
	y, e := strconv.ParseUint(b, 16, 64)
	if e != nil {
		return 0, e
	}
	return bits.OnesCount64(x ^ y), nil
}
