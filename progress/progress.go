package progress

import (
	"fmt"
	"time"
)

// Bar 进度条信息
type Bar struct {
	percent int64  //百分比
	cur     int64  //当前进度位置
	total   int64  //总进度
	rate    string //进度条
	graph   string //显示符号
}

func (bar *Bar) getPercent() int64 {
	return int64(float32(bar.cur) / float32(bar.total) * 100)
}

// NewOption 初始化
func (bar *Bar) NewOption(start, total int64) {
	bar.cur = start
	bar.total = total
	if bar.graph == "" {
		bar.graph = "█"
	}
	bar.percent = bar.getPercent()
	for i := 0; i < int(bar.percent); i += 2 {
		bar.rate += bar.graph //初始化进度条位置
	}
}

// NewOptionWithGraph 带参数设置的初始化函数
func (bar *Bar) NewOptionWithGraph(start, total int64, graph string) {
	bar.graph = graph
	bar.NewOption(start, total)
}

// Play 进度条展示
func (bar *Bar) Play(cur int64, beginTime time.Time) {
	bar.cur = cur
	last := bar.percent
	bar.percent = bar.getPercent()
	if bar.percent != last && bar.percent%2 == 0 {
		bar.rate += bar.graph
	}
	seconds := int(time.Since(beginTime).Seconds())
	hours := int(seconds) / 3600
	mins := int(seconds) / 60
	//hours := int(time.Since(beginTime).Hours())
	//mins := int(time.Since(beginTime).Minutes())
	fmt.Printf("\r[%-50s]%3d%%  %8d/%d 开始时间:[%s] 已用时间%02dh%02dm%02ds", bar.rate, bar.percent, bar.cur, bar.total, beginTime.Format("01-02 15:04:05"), hours, mins, seconds%60)
}

// Finish 结束进度条
func (bar *Bar) Finish() {
	fmt.Println()
}
