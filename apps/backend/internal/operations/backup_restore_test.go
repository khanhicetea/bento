package operations

import "testing"

func TestCrossAppWarning(t *testing.T) {
	if w := crossAppWarning("/b", "/b/shop/mysql-shop-20250101T000000Z.sql", "shop"); w != "" {
		t.Fatalf("same-app restore warned: %s", w)
	}
	if w := crossAppWarning("/b", "/b/other/mysql-other-20250101T000000Z.sql", "shop"); w == "" {
		t.Fatal("cross-app restore not warned")
	}
}
