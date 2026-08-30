package core

import "testing"

func TestCanWriteCustomer(t *testing.T) {
	c := Customer{Owner: "user2"}
	if !CanWriteCustomer(user("user1"), c) {
		t.Error("admin 应可写任意客户")
	}
	if !CanWriteCustomer(user("user2"), c) {
		t.Error("owner 应可写自己客户")
	}
	if CanWriteCustomer(user("user3"), c) {
		t.Error("其他经理不应可写")
	}
}

func TestCanWriteSupplier(t *testing.T) {
	if !CanWriteSupplier(user("user1")) {
		t.Error("admin 应可写供应商")
	}
	if CanWriteSupplier(user("user2")) {
		t.Error("经理不应可写供应商")
	}
}

func TestCanWriteMatch(t *testing.T) {
	m := Match{CreatedBy: "user3"}
	if !CanWriteMatch(user("user1"), m) {
		t.Error("admin 应可写任意匹配")
	}
	if !CanWriteMatch(user("user3"), m) {
		t.Error("创建者应可写自己匹配")
	}
	if CanWriteMatch(user("user2"), m) {
		t.Error("非创建者不应可写")
	}
}

func TestLookupUser(t *testing.T) {
	if _, ok := LookupUser("user1"); !ok {
		t.Error("user1 应存在")
	}
	if _, ok := LookupUser("nobody"); ok {
		t.Error("未知用户不应存在")
	}
}

func user(id string) User {
	u, _ := LookupUser(id)
	return u
}
