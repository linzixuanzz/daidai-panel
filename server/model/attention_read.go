package model

import "time"

// AttentionRead 是某个用户对「需要关注」某一条的已读记录（issue #186，v4.0.3）。
//
// 「需要关注」清单是按状态现算的（handler/system_attention.go），条目本身不落库，
// 所以已读只能记成「用户 + 条目 key + 版本」：
//   - Key 就是条目的 key（task_failing:12、dependency_failed:3、disk_high …）；
//   - Version 是用户标记时看到的那个版本（且与服务端当时的版本一致才写入）：一般是条目 at 换成 UTC 的 RFC3339 字符串
//     （用 UTC，改面板时区不会让已读全部失效），disk_high 用 level（warn / err）。
//
// key 已不在当前清单里的记录由 GET /system/attention 顺手删掉（问题消失后再出现就是新的一轮）。
//
// 读清单时 key 与 version 都对得上才算已读；问题再次发生（at 变了、磁盘从 warn 升到 err）版本就对不上，自动回到未读。
// 每个用户每个 key 只留一行（user_id + key 联合唯一），再次标记直接覆盖 Version。
type AttentionRead struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"not null;uniqueIndex:idx_attention_reads_user_key,priority:1" json:"user_id"`
	Key       string    `gorm:"size:128;not null;uniqueIndex:idx_attention_reads_user_key,priority:2" json:"key"`
	Version   string    `gorm:"size:64;not null;default:''" json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (AttentionRead) TableName() string {
	return "attention_reads"
}
