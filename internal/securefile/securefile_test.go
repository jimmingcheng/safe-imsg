package securefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadChecksFileAndParents(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(file, []byte(`{"safe":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerFile(file, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerFile(file, 5); err == nil {
		t.Fatal("oversized file accepted")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerFile(link, 100); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerFile(file, 100); err == nil {
		t.Fatal("client-replaceable file accepted")
	}
}

func TestAncestorSymlinkCannotHideWritableTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(target, "policy")
	if err := os.WriteFile(file, []byte("policy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerFile(filepath.Join(link, "policy"), 100); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerFile(filepath.Join(link, "policy"), 100); err == nil {
		t.Fatal("writable symlink target accepted")
	}
}

func TestAncestorSymlinkCannotHideWritableIntermediateDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	intermediate := filepath.Join(dir, "intermediate")
	for _, path := range []string{target, intermediate} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(target, "policy"), []byte("policy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../target", filepath.Join(intermediate, "next")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("intermediate/next", link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerFile(filepath.Join(link, "policy"), 100); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(intermediate, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwnerFile(filepath.Join(link, "policy"), 100); err == nil {
		t.Fatal("writable intermediate symlink directory accepted")
	}
}

func TestAncestorSymlinkCycleFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cycle")
	if err := os.Symlink("./cycle", link); err != nil {
		t.Fatal(err)
	}
	if err := CheckParents(filepath.Join(link, "policy")); err == nil {
		t.Fatal("symlink cycle accepted")
	}
}
