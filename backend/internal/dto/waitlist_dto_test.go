package dto

import (
	"strings"
	"testing"
	"time"

	"github.com/communitygarden/server/internal/model"
)

// wallDiff 比较两个时区下“当天墙上时钟”的差值（时:分:秒部分），用于验证 UTC+8 偏移。
func wallDiff(a, b time.Time) time.Duration {
	ha := time.Duration(a.Hour())*time.Hour + time.Duration(a.Minute())*time.Minute + time.Duration(a.Second())*time.Second
	hb := time.Duration(b.Hour())*time.Hour + time.Duration(b.Minute())*time.Minute + time.Duration(b.Second())*time.Second
	d := ha - hb
	if d < 0 {
		d += 24 * time.Hour
	}
	return d
}

// 跨时区契约：服务运行在 UTC、页面在上海（UTC+8）时，截止/登记时间必须以带时区的
// 绝对时刻下发；浏览器按本地时区渲染墙上时间，倒计时基于绝对时刻计算（不会多出 8 小时）。
func TestWaitlistDTO_TimeIsAbsoluteRFC3339(t *testing.T) {
	shanghai := time.FixedZone("UTC+8", 8*60*60)

	// 以当前 UTC 时间为锚（保证确认窗口未过期）：30 分钟前受邀，截止时间 = 现在 + 30 分钟
	now := time.Now().UTC().Truncate(time.Second)
	invited := now.Add(-30 * time.Minute)
	deadline := now.Add(30 * time.Minute)

	entry := &model.WaitlistEntry{
		ID: 1, PlotID: 2, UserID: 3,
		Status:           "invited",
		RegisteredAt:     invited.Add(-time.Hour),
		InvitedAt:        &invited,
		ConfirmExpiresAt: &deadline,
	}
	entry.RefreshActiveKey()

	out := ToWaitlistOutDTO(entry, 1)

	// 1) 下发的是带时区的 UTC 绝对时刻（Z 结尾），而不是无时区墙上时间
	wantDeadline := deadline.Format(time.RFC3339)
	if out.ConfirmExpiresAt != wantDeadline {
		t.Fatalf("confirm_expires_at = %q, want %q", out.ConfirmExpiresAt, wantDeadline)
	}
	if !strings.HasSuffix(out.ConfirmExpiresAt, "Z") || !strings.Contains(out.ConfirmExpiresAt, "T") {
		t.Fatalf("confirm_expires_at %q 不是带时区的 RFC3339 UTC 时刻", out.ConfirmExpiresAt)
	}
	if out.InvitedAt != invited.Format(time.RFC3339) {
		t.Fatalf("invited_at = %q, want %q", out.InvitedAt, invited.Format(time.RFC3339))
	}
	if out.RegisteredAt != invited.Add(-time.Hour).Format(time.RFC3339) {
		t.Fatalf("registered_at = %q, want %q", out.RegisteredAt, invited.Add(-time.Hour).Format(time.RFC3339))
	}

	// 2) 上海页面按本地时区渲染墙上时间：同一绝对时刻的墙上时钟恰好比 UTC 快 8 小时
	parsed, err := time.Parse(time.RFC3339, out.ConfirmExpiresAt)
	if err != nil {
		t.Fatalf("parse rfc3339: %v", err)
	}
	if wallDiff(parsed.In(shanghai), parsed.UTC()) != 8*time.Hour {
		t.Fatalf("shanghai vs UTC wall-clock diff = %v, want 8h", wallDiff(parsed.In(shanghai), parsed.UTC()))
	}
	regParsed, err := time.Parse(time.RFC3339, out.RegisteredAt)
	if err != nil {
		t.Fatalf("parse registered rfc3339: %v", err)
	}
	if wallDiff(regParsed.In(shanghai), regParsed.UTC()) != 8*time.Hour {
		t.Fatalf("registered shanghai vs UTC wall-clock diff != 8h")
	}
	if _, err := time.Parse("2006-01-02T15:04:05Z07:00", out.ConfirmExpiresAt); err != nil {
		t.Fatalf("confirm_expires_at not parseable as RFC3339: %v", err)
	}

	// 3) 倒计时按绝对时刻计算：剩余约 30 分钟，绝不出现旧实现的 +8 小时偏差
	remain := time.Until(parsed)
	if remain <= 29*time.Minute || remain > 30*time.Minute {
		t.Fatalf("remain = %v, want within (29m,30m]", remain)
	}

	// 4) 即便进程本地时区被设成上海，序列化仍固定为 UTC 绝对时刻（与服务器 TZ 无关）
	oldLocal := time.Local
	time.Local = shanghai
	defer func() { time.Local = oldLocal }()
	out2 := ToWaitlistOutDTO(entry, 1)
	if out2.ConfirmExpiresAt != wantDeadline {
		t.Fatalf("with Local=UTC+8 confirm_expires_at = %q, want %q", out2.ConfirmExpiresAt, wantDeadline)
	}
	if out2.RemainSeconds <= 0 || out2.RemainSeconds > int64((30*time.Minute).Seconds()) {
		t.Fatalf("remain_seconds = %d, want within (0,1800]", out2.RemainSeconds)
	}
}
