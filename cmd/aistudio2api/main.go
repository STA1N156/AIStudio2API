package main

import (
	"os"
	"runtime/debug"

	"github.com/Mag1cFall/AIStudio2API/internal/app"
)

// defaultGCPercent 为未设置 GOGC 时的回收阈值，堆增长到存活量的 1.5 倍即回收，压低常驻 WAA VM 带来的峰值内存
const defaultGCPercent = 50

// main 执行 aistudio2api 命令入口
func main() {
	if _, ok := os.LookupEnv("GOGC"); !ok {
		debug.SetGCPercent(defaultGCPercent)
	}
	os.Exit(app.Run(os.Args[1:]))
}
