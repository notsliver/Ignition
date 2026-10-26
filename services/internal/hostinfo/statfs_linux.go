package hostinfo

import "syscall"

func statfs(path string) (total, used int64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil || st.Blocks == 0 {
		return 0, 0, false
	}
	bs := int64(st.Bsize)
	total = int64(st.Blocks) * bs
	used = total - int64(st.Bfree)*bs
	return total, used, true
}
