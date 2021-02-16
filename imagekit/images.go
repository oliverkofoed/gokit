package imagekit

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"github.com/disintegration/imaging"
)

// GetThumbnail creates an image in the given size, trying to encode it to the given max bytes
func GetThumbnail(imageBytes []byte, width, height int, maxBytes int) ([]byte, string, error) {
	return process(imageBytes, maxBytes, func(image image.Image) image.Image {
		return imaging.Thumbnail(image, width, height, imaging.MitchellNetravali)
	})
}

// Resize resizes the image to the specified size, trying to encode it to the given max bytes
func Resize(imageBytes []byte, width, height int, maxBytes int) ([]byte, string, error) {
	return process(imageBytes, maxBytes, func(image image.Image) image.Image {
		return imaging.Resize(image, width, height, imaging.MitchellNetravali)
	})
}

// Fit scales down the image to fit in the bounding box
func Fit(imageBytes []byte, width, height int, maxBytes int) ([]byte, string, error) {
	return process(imageBytes, maxBytes, func(image image.Image) image.Image {
		return imaging.Fit(image, width, height, imaging.MitchellNetravali)
	})
}

// Fill creates an image with the specified dimensions and fills it with the scaled source image.
// To achieve the correct aspect ratio without stretching, the source image will be cropped.
func Fill(imageBytes []byte, width, height int, maxBytes int, anchor imaging.Anchor) ([]byte, string, error) {
	return process(imageBytes, maxBytes, func(image image.Image) image.Image {
		return imaging.Fill(image, width, height, anchor, imaging.MitchellNetravali)
	})
}

// FitRect scales the given dimensiosn to fit inside the maxWidth-maxHeight box.
func FitRect(width, height int, maxWidth, maxHeight int) (newWidth, newHeight int) {
	srcAspectRatio := float64(width) / float64(height)
	maxAspectRatio := float64(maxWidth) / float64(maxHeight)
	if srcAspectRatio > maxAspectRatio {
		newWidth = maxWidth
		newHeight = int(float64(newWidth) / srcAspectRatio)
	} else {
		newHeight = maxHeight
		newWidth = int(float64(newHeight) * srcAspectRatio)
	}
	return
}

func process(imageBytes []byte, maxBytes int, imageProcessor func(image image.Image) image.Image) ([]byte, string, error) {
	image, format, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, "", fmt.Errorf("Unable to decode image from %v bytes: %v", len(imageBytes), err)
	}

	result := imageProcessor(image)

	var buffer bytes.Buffer

	imageFormat, err := encodeImage(&buffer, format, result, maxBytes, 100)
	if err != nil {
		return nil, "", fmt.Errorf("Unable to encode rescaled image: %v", err)
	}

	mimeType, err := GetMimeType(imageFormat)
	if err != nil {
		return nil, "", fmt.Errorf("Unable to get mime type for format %v: %v", format, err)
	}

	return buffer.Bytes(), mimeType, nil
}

func encodeImage(buffer *bytes.Buffer, format string, image image.Image, maxBytes int, quality int) (string, error) {
	var err error
	switch format {
	case "png":
		err = png.Encode(buffer, image)
	case "gif":
		err = gif.Encode(buffer, image, nil)
	case "jpeg":
		err = jpeg.Encode(buffer, image, &jpeg.Options{Quality: quality})
	default:
		err = fmt.Errorf("Unknown image format: %v", format)
	}

	if err != nil {
		return "", err
	}

	if buffer.Len() > maxBytes && quality > 35 {
		format = "jpeg"
		buffer.Reset()
		return encodeImage(buffer, format, image, maxBytes, quality-15)
	}

	return format, nil
}

func ParseDetails(imageBytes []byte) (string, int, int, error) {
	c, imageFormat, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil {
		return "", 0, 0, fmt.Errorf("Clould not get mime type from image: %v", err)
	}

	mimeType, err := GetMimeType(imageFormat)
	if err != nil {
		return "", 0, 0, err
	}
	return mimeType, c.Width, c.Height, err
}

func ParseMimeType(imageBytes []byte) (string, error) {
	_, imageFormat, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil {
		return "", fmt.Errorf("Clould not get mime type from image: %v", err)
	}

	mimeType, err := GetMimeType(imageFormat)
	if err != nil {
		return "", err
	}
	return mimeType, err
}

// GetMimeType returns the mime type for the given image format cooresponding to registered image types from the image package.
func GetMimeType(imageFormat string) (string, error) {
	switch imageFormat {
	case "png":
		return "image/png", nil
	case "gif":
		return "image/gif", nil
	case "jpg":
	case "jpeg":
		return "image/jpeg", nil
	}
	return "", fmt.Errorf("Unknown image format: ", imageFormat)
}
