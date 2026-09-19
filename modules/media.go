package modules

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp"
)

var ErrUnsupportedMedia = errors.New("unsupported media type")

// AnonymizedImage is a raster file with identifying metadata removed.
type AnonymizedImage struct {
	Path         string
	Filename     string
	Mime         string
	Size         int64
	OriginalSize int64
	Format       string
	Orientation  int
	Stripped     []string
	Transcoded   bool
}

// AnonymizeImage strips EXIF/XMP/IPTC/ICC/comments and applies EXIF
// orientation so the pixels are upright without a metadata tag. JPEG and PNG
// keep their format when possible; WebP is transcoded to JPEG because the
// standard library cannot encode WebP.
func AnonymizeImage(srcPath, originalName string) (*AnonymizedImage, error) {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, err
	}
	if len(raw) < 12 {
		return nil, fmt.Errorf("%w: file is not a supported image", ErrUnsupportedMedia)
	}

	format := sniffImageFormat(raw, originalName)
	if format == "" {
		return nil, fmt.Errorf("%w: POST /media accepts JPEG, PNG, GIF, or WebP", ErrUnsupportedMedia)
	}

	stripped := detectImageMetadata(raw)
	orientation := 1
	outName := filepath.Base(originalName)
	mime := ""
	transcoded := false
	var out []byte

	switch format {
	case "jpeg":
		orientation = jpegOrientation(raw)
		if orientation >= 2 && orientation <= 8 {
			img, err := jpeg.Decode(bytes.NewReader(raw))
			if err != nil {
				return nil, fmt.Errorf("decode jpeg: %w", err)
			}
			img = applyOrientation(img, orientation)
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
				return nil, err
			}
			out = buf.Bytes()
			transcoded = true
		} else {
			out, err = jpegStripMetadata(raw)
			if err != nil {
				return nil, err
			}
		}
		outName = replaceExt(outName, ".jpg")
		mime = "image/jpeg"

	case "png":
		out, err = pngStripMetadata(raw)
		if err != nil {
			return nil, err
		}
		outName = replaceExt(outName, ".png")
		mime = "image/png"

	case "gif":
		out, err = gifAnonymize(raw)
		if err != nil {
			return nil, err
		}
		outName = replaceExt(outName, ".gif")
		mime = "image/gif"
		transcoded = true

	case "webp":
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("decode webp: %w", err)
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return nil, err
		}
		out = buf.Bytes()
		outName = replaceExt(outName, ".jpg")
		mime = "image/jpeg"
		transcoded = true
		stripped = uniqueStrings(append(stripped, "exif", "xmp"))

	default:
		return nil, fmt.Errorf("%w: POST /media accepts JPEG, PNG, GIF, or WebP", ErrUnsupportedMedia)
	}

	if err := os.MkdirAll(UploadTempDir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(UploadTempDir, "media-*")
	if err != nil {
		return nil, err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}

	return &AnonymizedImage{
		Path:         tmp.Name(),
		Filename:     outName,
		Mime:         mime,
		Size:         int64(len(out)),
		OriginalSize: int64(len(raw)),
		Format:       format,
		Orientation:  orientation,
		Stripped:     stripped,
		Transcoded:   transcoded,
	}, nil
}

func sniffImageFormat(raw []byte, name string) string {
	switch {
	case bytes.HasPrefix(raw, []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg"
	case bytes.HasPrefix(raw, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}):
		return "png"
	case bytes.HasPrefix(raw, []byte("GIF87a")) || bytes.HasPrefix(raw, []byte("GIF89a")):
		return "gif"
	case len(raw) >= 12 && bytes.Equal(raw[0:4], []byte("RIFF")) && bytes.Equal(raw[8:12], []byte("WEBP")):
		return "webp"
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".png":
		return "png"
	case ".gif":
		return "gif"
	case ".webp":
		return "webp"
	}
	return ""
}

func detectImageMetadata(raw []byte) []string {
	var found []string
	if bytes.Contains(raw, []byte("Exif\x00\x00")) || bytes.Contains(raw, []byte("eXIf")) {
		found = append(found, "exif")
	}
	if bytes.Contains(raw, []byte("http://ns.adobe.com/xap")) || bytes.Contains(raw, []byte("http://ns.adobe.com/xap/1.0/")) {
		found = append(found, "xmp")
	}
	if bytes.Contains(raw, []byte("ICC_PROFILE")) || bytes.Contains(raw, []byte("iCCP")) {
		found = append(found, "icc")
	}
	if bytes.Contains(raw, []byte("Photoshop 3.0")) || bytes.Contains(raw, []byte("8BIM")) {
		found = append(found, "iptc")
	}
	if bytes.Contains(raw, []byte("GPS")) || bytes.Contains(raw, []byte("gps")) {
		found = append(found, "gps")
	}
	if hasJPEGCOM(raw) || bytes.Contains(raw, []byte("tEXt")) || bytes.Contains(raw, []byte("iTXt")) || bytes.Contains(raw, []byte("zTXt")) {
		found = append(found, "text")
	}
	return uniqueStrings(found)
}

func hasJPEGCOM(raw []byte) bool {
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xD8 {
		return false
	}
	i := 2
	for i+1 < len(raw) {
		if raw[i] != 0xFF {
			return false
		}
		for i < len(raw) && raw[i] == 0xFF {
			i++
		}
		if i >= len(raw) {
			return false
		}
		marker := raw[i]
		i++
		if marker == 0xDA || marker == 0xD9 {
			return false
		}
		if marker == 0xFE {
			return true
		}
		if marker >= 0xD0 && marker <= 0xD7 || marker == 0x01 {
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		length := int(raw[i])<<8 | int(raw[i+1])
		if length < 2 || i+length > len(raw) {
			return false
		}
		i += length
	}
	return false
}

func jpegStripMetadata(data []byte) ([]byte, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, fmt.Errorf("%w: not a jpeg", ErrUnsupportedMedia)
	}
	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8})
	i := 2
	for i < len(data) {
		if data[i] != 0xFF {
			return nil, errors.New("invalid jpeg segment")
		}
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			break
		}
		marker := data[i]
		i++
		if marker == 0xD9 {
			out.Write([]byte{0xFF, 0xD9})
			break
		}
		if marker == 0xDA {
			out.Write([]byte{0xFF, 0xDA})
			if i <= len(data) {
				out.Write(data[i:])
			}
			return out.Bytes(), nil
		}
		if marker >= 0xD0 && marker <= 0xD7 || marker == 0x01 {
			continue
		}
		if i+1 >= len(data) {
			return nil, errors.New("truncated jpeg")
		}
		length := int(data[i])<<8 | int(data[i+1])
		if length < 2 || i+length > len(data) {
			return nil, errors.New("invalid jpeg segment length")
		}
		// Drop APP0–APP15 (EXIF, XMP, ICC, IPTC) and COM comments.
		if (marker >= 0xE0 && marker <= 0xEF) || marker == 0xFE {
			i += length
			continue
		}
		out.Write([]byte{0xFF, marker})
		out.Write(data[i : i+length])
		i += length
	}
	if out.Len() < 4 {
		return nil, errors.New("jpeg strip produced an empty file")
	}
	return out.Bytes(), nil
}

func jpegOrientation(data []byte) int {
	i := 2
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i+3 < len(data) {
		if data[i] != 0xFF {
			return 1
		}
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			return 1
		}
		marker := data[i]
		i++
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		if marker >= 0xD0 && marker <= 0xD7 || marker == 0x01 {
			continue
		}
		if i+1 >= len(data) {
			return 1
		}
		length := int(data[i])<<8 | int(data[i+1])
		if length < 2 || i+length > len(data) {
			return 1
		}
		payload := data[i+2 : i+length]
		i += length
		if marker != 0xE1 || !bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
			continue
		}
		if o := tiffOrientation(payload[6:]); o >= 1 && o <= 8 {
			return o
		}
	}
	return 1
}

func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch {
	case bytes.HasPrefix(tiff, []byte("II")):
		order = binary.LittleEndian
	case bytes.HasPrefix(tiff, []byte("MM")):
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	off := int(order.Uint32(tiff[4:8]))
	return walkTIFFOrientation(tiff, order, off, 0)
}

func walkTIFFOrientation(tiff []byte, order binary.ByteOrder, off, depth int) int {
	if depth > 4 || off < 0 || off+2 > len(tiff) {
		return 1
	}
	n := int(order.Uint16(tiff[off : off+2]))
	pos := off + 2
	for i := 0; i < n; i++ {
		if pos+12 > len(tiff) {
			return 1
		}
		tag := order.Uint16(tiff[pos : pos+2])
		typ := order.Uint16(tiff[pos+2 : pos+4])
		count := order.Uint32(tiff[pos+4 : pos+8])
		val := tiff[pos+8 : pos+12]
		if tag == 0x0112 && count >= 1 { // Orientation
			switch typ {
			case 3: // SHORT
				return int(order.Uint16(val[:2]))
			case 4: // LONG
				return int(order.Uint32(val))
			}
		}
		if tag == 0x8769 && typ == 4 && count == 1 { // Exif IFD pointer
			if o := walkTIFFOrientation(tiff, order, int(order.Uint32(val)), depth+1); o >= 2 {
				return o
			}
		}
		pos += 12
	}
	return 1
}

func pngStripMetadata(data []byte) ([]byte, error) {
	sig := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if !bytes.HasPrefix(data, sig) {
		return nil, fmt.Errorf("%w: not a png", ErrUnsupportedMedia)
	}
	keep := map[string]bool{
		"IHDR": true,
		"PLTE": true,
		"IDAT": true,
		"IEND": true,
		"tRNS": true,
	}
	var out bytes.Buffer
	out.Write(sig)
	i := 8
	sawIDAT := false
	for i+12 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[i : i+4]))
		typ := string(data[i+4 : i+8])
		end := i + 12 + length
		if end > len(data) {
			return nil, errors.New("invalid png chunk")
		}
		if keep[typ] {
			out.Write(data[i:end])
		}
		if typ == "IDAT" {
			sawIDAT = true
		}
		if typ == "IEND" {
			break
		}
		i = end
	}
	if !sawIDAT {
		return nil, errors.New("png is missing image data")
	}
	return out.Bytes(), nil
}

func gifAnonymize(data []byte) ([]byte, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode gif: %w", err)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func applyOrientation(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	switch o {
	case 5, 6, 7, 8:
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			switch o {
			case 2: // flip H
				dst.Set(w-1-x, y, c)
			case 3: // 180
				dst.Set(w-1-x, h-1-y, c)
			case 4: // flip V
				dst.Set(x, h-1-y, c)
			case 5: // transpose (flip H + rotate 270 CW)
				dst.Set(y, x, c)
			case 6: // 90 CW
				dst.Set(h-1-y, x, c)
			case 7: // transverse (flip H + rotate 90 CW)
				dst.Set(h-1-y, w-1-x, c)
			case 8: // 90 CCW
				dst.Set(y, w-1-x, c)
			}
		}
	}
	return dst
}

func replaceExt(name, ext string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	if base == "" || base == "." {
		base = "media"
	}
	return base + ext
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
