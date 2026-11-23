package typst

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
)

// ViewImage prepara una imagen PNG o JPEG para mostrarla a un agente: si su
// lado mayor supera maxSide se reduce (mismo filtro que las copias de la
// vista previa). Devuelve los bytes y su tipo MIME; si no hace falta reducirla
// devuelve el original.
func ViewImage(data []byte, maxSide int) ([]byte, string, error) {
	mime := ""
	switch {
	case bytes.HasPrefix(data, pngMagic):
		mime = "image/png"
	case bytes.HasPrefix(data, jpegMagic):
		mime = "image/jpeg"
	default:
		return nil, "", errors.New("formato no soportado (solo PNG o JPEG)")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	if cfg.Width <= maxSide && cfg.Height <= maxSide {
		return data, mime, nil
	}
	if cfg.Width*cfg.Height > proxyMaxSource {
		return nil, "", errors.New("imagen demasiado grande para procesarla en el servidor")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	dw, dh := maxSide, maxSide
	if cfg.Width >= cfg.Height {
		dh = max(1, cfg.Height*maxSide/cfg.Width)
	} else {
		dw = max(1, cfg.Width*maxSide/cfg.Height)
	}
	small := resize(img, dw, dh)
	var out bytes.Buffer
	if mime == "image/jpeg" {
		err = jpeg.Encode(&out, small, &jpeg.Options{Quality: 85})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, small)
	}
	return out.Bytes(), mime, err
}
