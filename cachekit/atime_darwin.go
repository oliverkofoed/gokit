package cachekit

import (
	"os"
	"syscall"
	"time"
)

func atime(info os.FileInfo) time.Time {
	stat := info.Sys().(*syscall.Stat_t)
	return time.Unix(int64(stat.Atimespec.Sec), int64(stat.Atimespec.Nsec))
}
