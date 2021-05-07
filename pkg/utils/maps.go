package utils

func MergeInto(src, dst map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}
