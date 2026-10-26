//go:build !linux

package hostinfo

func statfs(string) (int64, int64, bool) { return 0, 0, false }
