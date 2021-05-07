package utils

func MergeIntoStringMap(src, dst map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

func MergeIntoByteMap(src, dst map[string][]byte) {
	for k, v := range src {
		dst[k] = v
	}
}
