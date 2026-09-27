package domain

import "testing"

func TestValidateGitSource(t *testing.T) {
	good := []string{
		"git@github.com:owner/repo.git",
		"git@gitlab.example.com:group/sub/repo",
		"ssh://git@github.com/owner/repo.git",
		"ssh://git@git.example.com:2222/owner/repo.git",
		"https://github.com/owner/repo.git",
	}
	for _, u := range good {
		var errs ValidationErrors
		ValidateGitSource(u, "main", &errs)
		if errs.Err() != nil {
			t.Errorf("%s: %v", u, errs)
		}
	}
	bad := []string{
		"", "github.com/owner/repo", "http://github.com/o/r.git", "file:///etc", "ext::sh -c id",
		"-oProxyCommand=id", "--upload-pack=id", "git@github.com:../../etc", "git@-oProxy:o/r",
		"https://user:token@github.com/o/r.git", "https://token@github.com/o/r.git", "ssh://git:pw@github.com/o/r.git",
		"ssh://-oProxyCommand=id/o/r", "https://github.com/o/r.git?x=1", "git@github.com:o/r;id",
	}
	for _, u := range bad {
		var errs ValidationErrors
		ValidateGitSource(u, "main", &errs)
		if errs.Err() == nil {
			t.Errorf("accepted repo url %q", u)
		}
	}
	for _, b := range []string{"", "-f", "--orphan", "a..b", "/main", "main/", "x.lock", "a//b", "a b", "a~1", "a^", "a:b", "main."} {
		var errs ValidationErrors
		ValidateGitSource("git@github.com:o/r.git", b, &errs)
		if errs.Err() == nil {
			t.Errorf("accepted branch %q", b)
		}
	}
	for _, b := range []string{"main", "release/1.2", "feature_x-y"} {
		var errs ValidationErrors
		ValidateGitSource("git@github.com:o/r.git", b, &errs)
		if errs.Err() != nil {
			t.Errorf("rejected branch %q: %v", b, errs)
		}
	}
}
