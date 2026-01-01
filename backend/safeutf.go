package main

import "bytes"

func safeUTF8(s string) string {
	if s == "" {
		return ""
	}
	return string(bytes.ToValidUTF8([]byte(s), []byte{}))
}
