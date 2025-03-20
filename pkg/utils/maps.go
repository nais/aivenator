package utils

import "maps"

func MergeStringMap(src, dst map[string]string) map[string]string {
	target := make(map[string]string)
	maps.Copy(target, src)
	maps.Copy(target, dst)
	return target
}

func MergeByteMap(src, dst map[string][]byte) map[string][]byte {
	target := make(map[string][]byte)
	maps.Copy(target, src)
	maps.Copy(target, dst)
	return target
}

func KeysFromStringMap(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
