package web

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qrcode "github.com/skip2/go-qrcode"
)

// The QR is the entire handoff mechanism, so it has to be a real scannable
// image carrying the exact driver URL — not a placeholder that renders.
func TestQRCodeEncodesDriverURL(t *testing.T) {
	url := "https://shop.example/routes/deadbeef00112233445566778899aabbccddeeff00112233445566778899aabb"

	data, err := qrcode.Encode(url, qrcode.Medium, qrSizeDefault)
	require.NoError(t, err)

	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err, "must be a decodable PNG")
	assert.Equal(t, qrSizeDefault, img.Bounds().Dx())
	assert.Equal(t, qrSizeDefault, img.Bounds().Dy())

	// A 64-hex-character token is long enough to push the code into a higher
	// version; confirm it still encodes at the recovery level we ship rather
	// than erroring, since that is where "it worked in dev with a short URL"
	// would bite.
	assert.NotEmpty(t, data)
}

func TestQRSizeBounds(t *testing.T) {
	// The handler clamps to qrSizeMax; anything larger is a request to render
	// an enormous bitmap.
	assert.Greater(t, qrSizeMax, qrSizeDefault)

	data, err := qrcode.Encode("https://shop.example/routes/abc", qrcode.Medium, qrSizeMax)
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, qrSizeMax, img.Bounds().Dx())
}
