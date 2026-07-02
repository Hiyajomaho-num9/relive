package model

import (
	"strings"
	"time"
)

// PhotoTag 照片标签独立表
type PhotoTag struct {
	ID      uint   `gorm:"primarykey"`
	PhotoID uint   `gorm:"not null;uniqueIndex:idx_photo_tag_unique,priority:1"`
	Tag     string `gorm:"type:varchar(100);not null;index:idx_photo_tag_tag;uniqueIndex:idx_photo_tag_unique,priority:2"`
}

func (PhotoTag) TableName() string {
	return "photo_tags"
}

// PhotoTagStats 标签统计预聚合表（每标签一行）。
// 用于替代热门标签查询的实时 GROUP BY / COUNT(DISTINCT)，
// 由 photo_tags 的写入路径在同一事务内增量维护。
type PhotoTagStats struct {
	Tag        string    `gorm:"type:varchar(100);primaryKey" json:"tag"`
	PhotoCount int64     `gorm:"not null;default:0" json:"photo_count"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (PhotoTagStats) TableName() string {
	return "photo_tag_stats"
}

// SplitTags 将逗号分隔的标签字符串拆分为去重的切片
func SplitTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	seen := make(map[string]struct{}, len(parts))
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		result = append(result, p)
	}
	return result
}
