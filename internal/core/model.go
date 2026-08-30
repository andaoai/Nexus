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
