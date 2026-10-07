package extensions

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"unicode/utf16"

	"github.com/dop251/goja"
)

// installCrypto binds the CryptoJS subset marketplace extensions use. Its
// semantics are the ones they expect (not upstream CryptoJS's): AES-CBC/PKCS7, keys that aren't 16/24/32
// bytes are SHA-256'd, "Salted__" payloads use OpenSSL's EVP_BytesToKey.
func installCrypto(vm *goja.Runtime) {
	type encoder struct {
		parse     func(string) []byte
		stringify func([]byte) string
	}
	encoders := map[string]encoder{
		"Utf8": {func(s string) []byte { return []byte(s) }, func(b []byte) string { return string(b) }},
		"Base64": {func(s string) []byte {
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				b, err = base64.RawStdEncoding.DecodeString(s)
				if err != nil {
					return nil
				}
			}
			return b
		}, func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }},
		"Hex": {func(s string) []byte {
			b, err := hex.DecodeString(s)
			if err != nil {
				return nil
			}
			return b
		}, hex.EncodeToString},
		"Latin1": {func(s string) []byte {
			out := make([]byte, 0, len(s))
			for _, r := range s {
				out = append(out, byte(r))
			}
			return out
		}, func(b []byte) string {
			rs := make([]rune, len(b))
			for i, c := range b {
				rs[i] = rune(c)
			}
			return string(rs)
		}},
		"Utf16": {func(s string) []byte {
			var out []byte
			for _, u := range utf16.Encode([]rune(s)) {
				out = append(out, byte(u>>8), byte(u))
			}
			return out
		}, func(b []byte) string {
			u := make([]uint16, 0, len(b)/2)
			for i := 0; i+1 < len(b); i += 2 {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
			return string(utf16.Decode(u))
		}},
		"Utf16LE": {func(s string) []byte {
			var out []byte
			for _, u := range utf16.Encode([]rune(s)) {
				out = append(out, byte(u), byte(u>>8))
			}
			return out
		}, func(b []byte) string {
			u := make([]uint16, 0, len(b)/2)
			for i := 0; i+1 < len(b); i += 2 {
				u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
			}
			return string(utf16.Decode(u))
		}},
	}

	encObj := vm.NewObject()
	for name, e := range encoders {
		e := e
		o := vm.NewObject()
		_ = o.Set("__encoder", name)
		_ = o.Set("parse", func(s string) goja.Value {
			b := e.parse(s)
			if b == nil {
				return goja.Null()
			}
			return bytesToJS(vm, b)
		})
		_ = o.Set("stringify", func(v goja.Value) string { return e.stringify(valueToBytes(vm, v)) })
		_ = encObj.Set(name, o)
	}

	wordArray := func(data, iv []byte) goja.Value {
		o := vm.NewObject()
		_ = o.Set("__bytes", bytesToJS(vm, data))
		if iv != nil {
			_ = o.Set("iv", bytesToJS(vm, iv))
		}
		_ = o.Set("sigBytes", len(data))
		_ = o.Set("toString", func(call goja.FunctionCall) string {
			enc := call.Argument(0)
			if enc != nil && !goja.IsUndefined(enc) {
				if name := enc.ToObject(vm).Get("__encoder"); name != nil {
					if e, ok := encoders[name.String()]; ok {
						return e.stringify(data)
					}
				}
			}
			return base64.StdEncoding.EncodeToString(data)
		})
		return o
	}

	keyBytes := func(v goja.Value) []byte {
		if s, ok := v.Export().(string); ok {
			return []byte(s)
		}
		if obj, ok := v.(*goja.Object); ok {
			if b := obj.Get("__bytes"); b != nil && !goja.IsUndefined(b) {
				return valueToBytes(vm, b)
			}
		}
		return valueToBytes(vm, v)
	}
	ivFrom := func(cfg goja.Value) []byte {
		if cfg == nil || goja.IsUndefined(cfg) || goja.IsNull(cfg) {
			return nil
		}
		iv := cfg.ToObject(vm).Get("iv")
		if iv == nil || goja.IsUndefined(iv) || goja.IsNull(iv) {
			return nil
		}
		return keyBytes(iv)
	}

	aesObj := vm.NewObject()
	_ = aesObj.Set("encrypt", func(call goja.FunctionCall) goja.Value {
		msg := keyBytes(call.Argument(0))
		key := normKey(keyBytes(call.Argument(1)))
		iv := ivFrom(call.Argument(2))
		prepend := false
		if iv == nil {
			iv = make([]byte, aes.BlockSize)
			_, _ = rand.Read(iv)
			prepend = true
		}
		ct, err := aesCBCEncrypt(msg, key, iv)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if prepend {
			ct = append(append([]byte(nil), iv...), ct...)
		}
		return wordArray(ct, iv)
	})
	_ = aesObj.Set("decrypt", func(call goja.FunctionCall) goja.Value {
		var data []byte
		msg := call.Argument(0)
		if s, ok := msg.Export().(string); ok {
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				b, err = base64.RawStdEncoding.DecodeString(s)
				if err != nil {
					panic(vm.NewGoError(errors.New("invalid base64 ciphertext")))
				}
			}
			data = b
		} else {
			data = keyBytes(msg)
		}
		rawKey := keyBytes(call.Argument(1))
		iv := ivFrom(call.Argument(2))
		var key []byte
		switch {
		case iv != nil:
			key = normKey(rawKey)
		case len(data) >= 16 && bytes.Equal(data[:8], []byte("Salted__")):
			salt := data[8:16]
			k, i := evpBytesToKey(rawKey, salt, 32, 16)
			key, iv, data = k, i, data[16:]
		default:
			if len(data) < aes.BlockSize {
				panic(vm.NewGoError(errors.New("ciphertext too short")))
			}
			key = normKey(rawKey)
			iv, data = data[:aes.BlockSize], data[aes.BlockSize:]
		}
		pt, err := aesCBCDecrypt(data, key, iv)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return wordArray(pt, nil)
	})

	crypto := vm.NewObject()
	_ = crypto.Set("AES", aesObj)
	_ = crypto.Set("enc", encObj)
	_ = vm.Set("CryptoJS", crypto)
}

func normKey(k []byte) []byte {
	switch len(k) {
	case 16, 24, 32:
		return k
	}
	sum := sha256.Sum256(k)
	return sum[:]
}

func aesCBCEncrypt(pt, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) < aes.BlockSize {
		return nil, errors.New("invalid IV")
	}
	pad := aes.BlockSize - len(pt)%aes.BlockSize
	pt = append(append([]byte(nil), pt...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(pt))
	cipher.NewCBCEncrypter(block, iv[:aes.BlockSize]).CryptBlocks(out, pt)
	return out, nil
}

func aesCBCDecrypt(ct, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("ciphertext is not a multiple of the block size")
	}
	if len(iv) < aes.BlockSize {
		return nil, errors.New("invalid IV")
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv[:aes.BlockSize]).CryptBlocks(out, ct)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return out, nil
	}
	return out[:len(out)-pad], nil
}

// evpBytesToKey implements OpenSSL's key derivation (MD5, 1 iteration).
func evpBytesToKey(password, salt []byte, keyLen, ivLen int) ([]byte, []byte) {
	var d, prev []byte
	for len(d) < keyLen+ivLen {
		h := md5.New()
		h.Write(prev)
		h.Write(password)
		h.Write(salt)
		prev = h.Sum(nil)
		d = append(d, prev...)
	}
	return d[:keyLen], d[keyLen : keyLen+ivLen]
}
