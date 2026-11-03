package backup

import (
	"bytes"
	"strings"
	"testing"
)

func TestDefinerFilterStripsDefinersAndPassesData(t *testing.T) {
	in := "/*!50013 DEFINER=`root`@`%` SQL SECURITY DEFINER */\n" +
		"CREATE DEFINER=`app`@`localhost` PROCEDURE p() BEGIN END;;\n" +
		"/*!50017 DEFINER=`root`@`%`*/ /*!50003 TRIGGER t */\n" +
		"INSERT INTO `t` VALUES ('DEFINER=`x`@`y`');\n" +
		"INSERT INTO `t` VALUES ('see DEFINER=root@localhost here'),('DEFINER=a@b');\n" +
		"/*!50106 CREATE*/ /*!50117 DEFINER=`root`@`localhost`*/ /*!50106 EVENT e ON SCHEDULE EVERY 1 DAY DO SELECT 1 */ ;;\n" +
		"-- tail no newline"
	want := "/*!50013 SQL SECURITY DEFINER */\n" +
		"CREATE PROCEDURE p() BEGIN END;;\n" +
		"/*!50017*/ /*!50003 TRIGGER t */\n" +
		"INSERT INTO `t` VALUES ('DEFINER=`x`@`y`');\n" +
		"INSERT INTO `t` VALUES ('see DEFINER=root@localhost here'),('DEFINER=a@b');\n" +
		"/*!50106 CREATE*/ /*!50117*/ /*!50106 EVENT e ON SCHEDULE EVERY 1 DAY DO SELECT 1 */ ;;\n" +
		"-- tail no newline"
	for _, size := range []int{1, 7, 13, 64, len(in)} {
		var out bytes.Buffer
		f := &definerFilter{w: &out}
		for i := 0; i < len(in); i += size {
			if _, err := f.Write([]byte(in[i:min(i+size, len(in))])); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.Flush(); err != nil {
			t.Fatal(err)
		}
		if out.String() != want {
			t.Fatalf("chunk %d: got\n%s\nwant\n%s", size, out.String(), want)
		}
	}
	// One write per line, as a dump of short rows arrives from Docker.
	var out bytes.Buffer
	f := &definerFilter{w: &out}
	for line := range strings.Lines(in) {
		if _, err := f.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Flush(); err != nil || out.String() != want {
		t.Fatalf("line writes: got\n%s\nwant\n%s (%v)", out.String(), want, err)
	}
}
