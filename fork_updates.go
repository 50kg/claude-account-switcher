package main

import (
	"context"
	"fmt"
)

// Chinese release builds set this to the fork's download page. They use manual
// updates so the upstream installer cannot replace them with an English build.
// Leave it empty for the standard upstream update behavior.
var manualUpdateURL = ""

func updateMenuText(v string) (title, tooltip string) {
	if manualUpdateURL != "" {
		return tr("Download Chinese edition updates…", "下载中文版更新…"),
			tr("Open the Chinese edition's download page", "打开中文版发布页面，手动下载更新")
	}
	if v != "" {
		title = fmt.Sprintf(tr("Version %s is out, click to update", "发现新版本 %s，点击更新"), v)
	}
	return title, tr("Download the new version and restart on it", "下载新版本并重启切换器")
}

func (s *switcher) requestUpdate(ctx context.Context, open func(string)) {
	if manualUpdateURL != "" {
		open(manualUpdateURL)
		return
	}
	if v := s.snapshot().Update; v != "" {
		s.installUpdate(ctx, v)
	}
}
