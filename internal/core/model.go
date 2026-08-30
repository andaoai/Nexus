// Package core 领域模型与纯函数，不依赖 HTTP/git。
package core

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"time"
)

// Customer 客户。
type Customer struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Contact      string    `json:"contact"`
	Phone        string    `json:"phone"`
	Email        string    `json:"email"`
	Requirements string    `json:"requirements"`
	TechStack    string    `json:"tech_stack"`
	Industry     string    `json:"industry"`
	Owner        string    `json:"owner"`    // 负责的客户经理 user id
	Status       string    `json:"status"`   // 跟进中/已报价/已成交/已流失
	Priority     int       `json:"priority"` // 1-5
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// StatusCustomer 客户状态集合。
var StatusCustomer = []string{"跟进中", "已报价", "已成交", "已流失"}

// Supplier 供应商。
type Supplier struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Contact        string    `json:"contact"`
	Phone          string    `json:"phone"`
	Specialties    []string  `json:"specialties"`
	PriceLevel     string    `json:"price_level"`
	DeliverySpeed  int       `json:"delivery_speed"`  // 1-5
	QualityRating  float64   `json:"quality_rating"`  // 0-5
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

// Solution 技术方案。
type Solution struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	TechStack     []string  `json:"tech_stack"`
	EstimatedCost float64   `json:"estimated_cost"`
	DeliveryDays  int       `json:"delivery_days"`
	SupplierID    string    `json:"supplier_id"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

// Match 需求-方案匹配记录。
type Match struct {
	ID           string    `json:"id"`
	CustomerID   string    `json:"customer_id"`
	SolutionID   string    `json:"solution_id"`
	SupplierID   string    `json:"supplier_id"`
	MatchScore   int       `json:"match_score"` // 0-100
	MatchReason  string    `json:"match_reason"`
	Status       string    `json:"status"` // 待确认/已确认/已签约/已放弃
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

// StatusMatch 匹配状态集合。
var StatusMatch = []string{"待确认", "已确认", "已签约", "已放弃"}

// Message 聊天消息。Role: user（执行者发言）/ assistant（AI 回复）/ system（错误标记等）。
type Message struct {
	Role    string    `json:"role"`
	Author  string    `json:"author"` // user id 或 "ai"
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

// Conversation 一次围绕客户/供应商/通用主题的持续 AI 聊天会话。
// 存储为 conversations/<owner>/<id>.json，整文件覆盖提交。
type Conversation struct {
	ID              string    `json:"id"`
	Owner           string    `json:"owner"` // 会话创建者 user id
	SubjectType     string    `json:"subject_type"` // customer / supplier / general
	SubjectID       string    `json:"subject_id"`   // 关联实体 id，general 为空
	SubjectName     string    `json:"subject_name"` // 冗余快照，列表展示用
	Title           string    `json:"title"`
	Skill           string    `json:"skill"` // 使用的技能名（skills/<name>.md）
	ClaudeSessionID string    `json:"claude_session_id"` // claude CLI session，--resume 续接
	Summary         string    `json:"summary"`           // AI 生成的进展摘要
	SummaryAt       time.Time `json:"summary_at"`
	Messages        []Message `json:"messages"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SubjectTypes 会话主题类型集合。
var SubjectTypes = []string{"customer", "supplier", "general"}

// ID 生成：前缀 + 6 位随机 base32（小写，去掉易混淆字符）。
func NewID(prefix string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	b := make([]byte, 6)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[n.Int64()]
	}
	return prefix + "-" + string(b)
}

// JSON 实体序列化为带换行的 JSON 文件内容。
func JSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
