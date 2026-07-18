package llm

import (
	"encoding/base64"
	"mime"
	"net/url"
	"path"
	"strings"
)

const defaultImageMediaType = "image/jpeg"

type imageInput struct {
	URL         string
	MediaType   string
	Data        []byte
	EncodedData string
}

func imageInputFromURL(value string) (imageInput, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return imageInput{}, false
	}
	if input, ok := dataImageInput(value); ok {
		return input, true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return imageInput{}, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return imageInput{}, false
	}
	return imageInput{
		URL:       value,
		MediaType: imageMediaTypeFromPath(parsed.Path),
	}, true
}

func dataImageInput(value string) (imageInput, bool) {
	rest, ok := strings.CutPrefix(value, "data:")
	if !ok {
		return imageInput{}, false
	}
	header, payload, ok := strings.Cut(rest, ",")
	if !ok {
		return imageInput{}, false
	}
	headerParts := strings.Split(header, ";")
	mediaType := strings.ToLower(strings.TrimSpace(headerParts[0]))
	if !strings.HasPrefix(mediaType, "image/") {
		return imageInput{}, false
	}
	base64Encoded := false
	for _, part := range headerParts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			base64Encoded = true
			break
		}
	}
	if !base64Encoded {
		return imageInput{}, false
	}
	unescaped, err := url.PathUnescape(payload)
	if err != nil {
		return imageInput{}, false
	}
	data, ok := decodeBase64Image(unescaped)
	if !ok {
		return imageInput{}, false
	}
	return imageInput{
		MediaType:   mediaType,
		Data:        data,
		EncodedData: base64.StdEncoding.EncodeToString(data),
	}, true
}

func decodeBase64Image(value string) ([]byte, bool) {
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' || r == ' ' {
			return -1
		}
		return r
	}, value)
	if value == "" {
		return nil, false
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		data, err := encoding.DecodeString(value)
		if err == nil {
			return data, true
		}
	}
	return nil, false
}

func imageMediaTypeFromPath(filePath string) string {
	ext := strings.ToLower(path.Ext(filePath))
	if ext == ".jpg" {
		ext = ".jpeg"
	}
	if ext != "" {
		if mediaType := mime.TypeByExtension(ext); strings.HasPrefix(mediaType, "image/") {
			if value, _, ok := strings.Cut(mediaType, ";"); ok {
				return strings.TrimSpace(value)
			}
			return mediaType
		}
	}
	switch ext {
	case ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return defaultImageMediaType
	}
}
