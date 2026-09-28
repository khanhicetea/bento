package backup

import "testing"

func TestMySQLCharsetClause(t *testing.T) {
	def := "CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"
	cases := map[string]string{
		"":                               def,
		"/*!40101 SET NAMES utf8mb4 */;": def,
		"/*!40101 SET NAMES latin1 */;":  "CHARACTER SET latin1",
		"SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;": "CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		"SET NAMES utf8;\nCREATE DATABASE /*!32312 IF NOT EXISTS*/ `x` /*!40100 DEFAULT CHARACTER SET utf8mb3 COLLATE utf8mb3_general_ci */;": "CHARACTER SET utf8mb3 COLLATE utf8mb3_general_ci",
		"SET NAMES binary;":    def,
		"SET NAMES `x; DROP`;": def,
	}
	for in, want := range cases {
		if got := mysqlCharsetClause([]byte(in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
