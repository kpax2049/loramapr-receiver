package meshcore

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	routeTransportFlood  = byte(0)
	routeFlood           = byte(1)
	routeDirect          = byte(2)
	routeTransportDirect = byte(3)

	payloadTypeAdvert = byte(4)
	payloadVersionOne = byte(0)

	advertLocationFlag = byte(0x10)
	advertFeature1Flag = byte(0x20)
	advertFeature2Flag = byte(0x40)
	advertNameFlag     = byte(0x80)
	advertMaxAppData   = 32
)

var ErrInvalidOnAirPacket = errors.New("invalid MeshCore on-air packet")

type wirePacket struct {
	routeKind string
	path      []string
	version   byte
	typeCode  byte
	payload   []byte
}

type signedAdvert struct {
	publicKey     []byte
	timestamp     uint32
	latitudeE6    int32
	longitudeE6   int32
	hasPosition   bool
	name          string
	signingBytes  []byte
	signingSHA256 string
	onAirSHA256   string
}

func parseWirePacket(raw []byte) (wirePacket, error) {
	if len(raw) < 3 {
		return wirePacket{}, fmt.Errorf("%w: packet is shorter than header, path encoding, and payload", ErrInvalidOnAirPacket)
	}

	header := raw[0]
	route := header & 0x03
	index := 1
	if route == routeTransportFlood || route == routeTransportDirect {
		if len(raw) < index+4+1 {
			return wirePacket{}, fmt.Errorf("%w: transport codes are truncated", ErrInvalidOnAirPacket)
		}
		index += 4
	}

	pathEncoding := raw[index]
	index++
	hashCount := int(pathEncoding & 0x3f)
	hashSize := int(pathEncoding>>6) + 1
	if hashSize == 4 || hashCount*hashSize > 64 {
		return wirePacket{}, fmt.Errorf("%w: path encoding 0x%02x is reserved or exceeds 64 bytes", ErrInvalidOnAirPacket, pathEncoding)
	}
	pathBytes := hashCount * hashSize
	if len(raw) <= index+pathBytes {
		return wirePacket{}, fmt.Errorf("%w: path or payload is truncated", ErrInvalidOnAirPacket)
	}
	path := make([]string, 0, hashCount)
	for offset := 0; offset < pathBytes; offset += hashSize {
		path = append(path, hex.EncodeToString(raw[index+offset:index+offset+hashSize]))
	}
	index += pathBytes

	routeKind := "unknown"
	switch route {
	case routeTransportFlood, routeFlood:
		routeKind = "flood"
	case routeDirect, routeTransportDirect:
		routeKind = "direct"
	}
	return wirePacket{
		routeKind: routeKind,
		path:      path,
		version:   (header >> 6) & 0x03,
		typeCode:  (header >> 2) & 0x0f,
		payload:   append([]byte(nil), raw[index:]...),
	}, nil
}

func parseAndVerifySignedAdvert(onAir []byte) (wirePacket, signedAdvert, error) {
	packet, err := parseWirePacket(onAir)
	if err != nil {
		return wirePacket{}, signedAdvert{}, err
	}
	if packet.version != payloadVersionOne {
		return packet, signedAdvert{}, fmt.Errorf("%w: payload version is %d, want 1", ErrInvalidOnAirPacket, packet.version+1)
	}
	if packet.typeCode != payloadTypeAdvert {
		return packet, signedAdvert{}, fmt.Errorf("%w: payload type is %d, want ADVERT", ErrInvalidOnAirPacket, packet.typeCode)
	}
	if len(packet.payload) < ed25519.PublicKeySize+4+ed25519.SignatureSize+1 ||
		len(packet.payload) > ed25519.PublicKeySize+4+ed25519.SignatureSize+advertMaxAppData {
		return packet, signedAdvert{}, fmt.Errorf("%w: advert payload length %d is outside 101..132", ErrInvalidOnAirPacket, len(packet.payload))
	}

	publicKey := append([]byte(nil), packet.payload[:ed25519.PublicKeySize]...)
	wireTimestamp := packet.payload[ed25519.PublicKeySize : ed25519.PublicKeySize+4]
	signatureStart := ed25519.PublicKeySize + 4
	signature := packet.payload[signatureStart : signatureStart+ed25519.SignatureSize]
	appData := packet.payload[signatureStart+ed25519.SignatureSize:]
	advert, err := parseAdvertAppData(appData)
	if err != nil {
		return packet, signedAdvert{}, err
	}

	signingBytes := make([]byte, 0, len(publicKey)+len(wireTimestamp)+len(appData))
	signingBytes = append(signingBytes, publicKey...)
	signingBytes = append(signingBytes, wireTimestamp...)
	signingBytes = append(signingBytes, appData...)
	if !ed25519.Verify(ed25519.PublicKey(publicKey), signingBytes, signature) {
		return packet, signedAdvert{}, fmt.Errorf("%w: advert Ed25519 signature verification failed", ErrInvalidOnAirPacket)
	}

	return packet, signedAdvert{
		publicKey:     publicKey,
		timestamp:     binary.LittleEndian.Uint32(wireTimestamp),
		latitudeE6:    advert.latitudeE6,
		longitudeE6:   advert.longitudeE6,
		hasPosition:   advert.hasPosition,
		name:          advert.name,
		signingBytes:  signingBytes,
		signingSHA256: sha256Hex(signingBytes),
		onAirSHA256:   sha256Hex(onAir),
	}, nil
}

type advertAppData struct {
	latitudeE6  int32
	longitudeE6 int32
	hasPosition bool
	name        string
}

func parseAdvertAppData(appData []byte) (advertAppData, error) {
	if len(appData) < 1 || len(appData) > advertMaxAppData {
		return advertAppData{}, fmt.Errorf("%w: advert application data length %d is outside 1..32", ErrInvalidOnAirPacket, len(appData))
	}
	flags := appData[0]
	advertType := flags & 0x0f
	if advertType < 1 || advertType > 4 {
		return advertAppData{}, fmt.Errorf("%w: advert application type %d is unsupported", ErrInvalidOnAirPacket, advertType)
	}

	result := advertAppData{}
	index := 1
	if flags&advertLocationFlag != 0 {
		if len(appData) < index+8 {
			return advertAppData{}, fmt.Errorf("%w: advert location is truncated", ErrInvalidOnAirPacket)
		}
		result.hasPosition = true
		result.latitudeE6 = int32(binary.LittleEndian.Uint32(appData[index : index+4]))
		result.longitudeE6 = int32(binary.LittleEndian.Uint32(appData[index+4 : index+8]))
		if result.latitudeE6 < -90_000_000 || result.latitudeE6 > 90_000_000 ||
			result.longitudeE6 < -180_000_000 || result.longitudeE6 > 180_000_000 {
			return advertAppData{}, fmt.Errorf("%w: advert coordinates are out of range", ErrInvalidOnAirPacket)
		}
		index += 8
	}
	if flags&advertFeature1Flag != 0 {
		index += 2
	}
	if flags&advertFeature2Flag != 0 {
		index += 2
	}
	if index > len(appData) {
		return advertAppData{}, fmt.Errorf("%w: advert feature fields are truncated", ErrInvalidOnAirPacket)
	}

	if flags&advertNameFlag != 0 {
		if index == len(appData) {
			return advertAppData{}, fmt.Errorf("%w: advert name flag has no name bytes", ErrInvalidOnAirPacket)
		}
		name := appData[index:]
		if !utf8.Valid(name) {
			return advertAppData{}, fmt.Errorf("%w: advert name is not valid UTF-8", ErrInvalidOnAirPacket)
		}
		result.name = string(name)
		index = len(appData)
	}
	if index != len(appData) {
		return advertAppData{}, fmt.Errorf("%w: advert contains trailing bytes without the name flag", ErrInvalidOnAirPacket)
	}
	return result, nil
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
