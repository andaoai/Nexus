package core

// Role 用户角色。
type Role string

const (
	Admin   Role = "admin"   // user1：全部读写
	Manager Role = "manager" // user2/user3：客户经理
)

// User 用户（3 人小团队，编译期配置）。
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// Users 内置用户表。
var Users = map[string]User{
	"user1": {ID: "user1", Name: "管理员", Role: Admin},
	"user2": {ID: "user2", Name: "经理A", Role: Manager},
	"user3": {ID: "user3", Name: "经理B", Role: Manager},
}

// LookupUser 按 ID 查用户。
func LookupUser(id string) (User, bool) {
	u, ok := Users[id]
	return u, ok
}

// CanWriteCustomer admin 任意；manager 仅自己 owner 的客户。
func CanWriteCustomer(u User, c Customer) bool {
	if u.Role == Admin {
		return true
	}
	return c.Owner == u.ID
}

// CanWriteSupplier 仅 admin。
func CanWriteSupplier(u User) bool {
	return u.Role == Admin
}

// CanWriteSolution 仅 admin。
func CanWriteSolution(u User) bool {
	return u.Role == Admin
}

// CanWriteMatch admin 任意；manager 仅 created_by 是自己。
func CanWriteMatch(u User, m Match) bool {
	if u.Role == Admin {
		return true
	}
	return m.CreatedBy == u.ID
}

// IsAdmin 是否管理员。
func IsAdmin(u User) bool {
	return u.Role == Admin
}
