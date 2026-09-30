package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishRefusesEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".partial-x")
	os.WriteFile(p, nil, 0o600)
	if _, err := publish(p, filepath.Join(dir, "final.sql")); err == nil {
		t.Fatal("empty artifact must not be published")
	}
	if _, err := os.Stat(filepath.Join(dir, "final.sql")); err == nil {
		t.Fatal("final artifact exists")
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("partial not cleaned")
	}
}

func TestRetentionKeepsNewestPerDatabaseAndSchedule(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "shop"), 0o700)
	for _, n := range []string{
		"mysql-shop-20250101T000000.000Z~daily.sql.zst", "mysql-shop-20250102T000000.000Z~daily.sql.zst",
		"mysql-shop-20250103T000000.000Z~daily.sql.zst",
		"mysql-shop-20250101T000000.000Z~hourly.sql.zst", "mysql-shop-20250102T000000.000Z~hourly.sql.zst",
		"mysql-shop-20250100T000000.000Z.sql.zst", // manual: never pruned
		"mysql-other-20250101T000000.000Z~daily.sql.zst", ".partial-abc",
	} {
		os.WriteFile(filepath.Join(dir, "shop", n), []byte("x"), 0o600)
	}
	arts, err := ListArtifacts(dir)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, a := range arts {
		if a.Database == "shop" {
			keys[RetentionKey(a)] = true
		}
	}
	removed, err := RetainKeys(dir, 2, keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "shop/mysql-shop-20250101T000000.000Z~daily.sql.zst" {
		t.Fatalf("removed %v", removed)
	}
	if ScheduleTag("backup-Daily_1") != "backupdaily1" {
		t.Fatal(ScheduleTag("backup-Daily_1"))
	}
}

func TestResolveArtifactContainment(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "shop"), 0o700)
	os.WriteFile(filepath.Join(dir, "shop", "mysql-shop-20250101T000000.000Z.sql"), []byte("x"), 0o600)
	os.Symlink("/etc", filepath.Join(dir, "evil"))
	if _, err := ResolveArtifact(dir, "shop/mysql-shop-20250101T000000.000Z.sql"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x", "/etc/passwd", "evil/passwd", "shop/.partial", "shop"} {
		if _, err := ResolveArtifact(dir, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPublishRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "final.sql")
	os.WriteFile(final, []byte("old"), 0o600)
	p := filepath.Join(dir, ".partial-y")
	os.WriteFile(p, []byte("new"), 0o600)
	if _, err := publish(p, final); err == nil {
		t.Fatal("existing artifact must not be overwritten")
	}
	if b, _ := os.ReadFile(final); string(b) != "old" {
		t.Fatalf("artifact overwritten: %q", b)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("partial not cleaned")
	}
	p2 := filepath.Join(dir, ".partial-z")
	os.WriteFile(p2, []byte("data"), 0o600)
	if n, err := publish(p2, filepath.Join(dir, "other.sql")); err != nil || n != 4 {
		t.Fatalf("publish: %d %v", n, err)
	}
	if _, err := os.Stat(p2); err == nil {
		t.Fatal("partial left behind after publish")
	}
}

func TestListArtifactsMillisecondNamesOnly(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "shop"), 0o700)
	for _, n := range []string{"mysql-shop-20250101T000000Z.sql.zst", "mysql-shop-20250101T000000.123Z.sql.zst", "mysql-shop-20250101T000000.500Z.sql"} {
		os.WriteFile(filepath.Join(dir, "shop", n), []byte("x"), 0o600)
	}
	arts, err := ListArtifacts(dir)
	if err != nil || len(arts) != 2 {
		t.Fatalf("%v %v", arts, err)
	}
	if arts[0].Path != "shop/mysql-shop-20250101T000000.500Z.sql" || arts[1].Path != "shop/mysql-shop-20250101T000000.123Z.sql.zst" {
		t.Fatalf("order %v", arts)
	}
	for _, a := range arts {
		if a.Database != "shop" || a.CreatedAt.IsZero() {
			t.Fatalf("bad parse %+v", a)
		}
	}
}

func TestDefinerFilterStripsDefinersAndPassesData(t *testing.T) {
	in := "/*!50013 DEFINER=`root`@`%` SQL SECURITY DEFINER */\n" +
		"CREATE DEFINER=`app`@`localhost` PROCEDURE p() BEGIN END;;\n" +
		"/*!50017 DEFINER=`root`@`%`*/ /*!50003 TRIGGER t */\n" +
		"INSERT INTO `t` VALUES ('DEFINER=`x`@`y`');\n" +
		"-- tail no newline"
	var out bytes.Buffer
	f := &definerFilter{w: &out}
	for i := 0; i < len(in); i += 7 {
		end := min(i+7, len(in))
		if _, err := f.Write([]byte(in[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "/*!50013 SQL SECURITY DEFINER */\n" +
		"CREATE PROCEDURE p() BEGIN END;;\n" +
		"/*!50017*/ /*!50003 TRIGGER t */\n" +
		"INSERT INTO `t` VALUES ('DEFINER=`x`@`y`');\n" +
		"-- tail no newline"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}
