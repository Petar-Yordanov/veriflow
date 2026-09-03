package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	stdout := flag.Bool("stdout", false, "print the bundle instead of copying it")
	output := flag.String("output", "", "also save the bundle to FILE")
	allowSensitive := flag.Bool("allow-sensitive", false, "allow explicitly selected sensitive files")
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: read-into-clipboard [options] PROJECT_ROOT FILE [FILE ...]")
		os.Exit(2)
	}
	root, err := filepath.Abs(args[0])
	must(err)
	root = filepath.Clean(root)
	var bundle bytes.Buffer
	seen := map[string]bool{}
	count := 0
	for _, requested := range args[1:] {
		if strings.ContainsAny(requested, "\n\t") {
			die("tabs and newlines are not supported in selected paths: %s", requested)
		}
		candidate := requested
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		abs, err := filepath.Abs(candidate)
		must(err)
		abs = filepath.Clean(abs)
		if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
			die("selected path is outside the project root: %s", requested)
		}
		info, err := os.Lstat(abs)
		if err != nil {
			die("selected file does not exist: %s", requested)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			die("symlinks are not read: %s", requested)
		}
		if !info.Mode().IsRegular() {
			die("selected path is not a regular file: %s", requested)
		}
		rel, _ := filepath.Rel(root, abs)
		if !*allowSensitive && isSensitive(rel) {
			die("refusing potentially sensitive file: %s (use --allow-sensitive intentionally)", rel)
		}
		data, err := os.ReadFile(abs)
		must(err)
		if bytes.IndexByte(data, 0) >= 0 {
			die("selected file appears to be binary: %s", rel)
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		count++
		fmt.Fprintf(&bundle, "===== FILE: %s =====\n", filepath.ToSlash(rel))
		bundle.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			bundle.WriteByte('\n')
		}
		fmt.Fprintf(&bundle, "===== End of file: %s =====\n\n", filepath.ToSlash(rel))
	}
	if *output != "" {
		must(os.MkdirAll(filepath.Dir(*output), 0755))
		must(os.WriteFile(*output, bundle.Bytes(), 0644))
	}
	if *stdout {
		_, _ = os.Stdout.Write(bundle.Bytes())
	} else {
		must(copyClipboard(bundle.Bytes()))
	}
	fmt.Fprintf(os.Stderr, "Read %d selected files (%d bytes) from: %s\n", count, bundle.Len(), root)
}
func isSensitive(rel string) bool {
	name := strings.ToLower(filepath.Base(rel))
	if name == ".env" || strings.HasPrefix(name, ".env.") {
		return name != ".env.example" && name != ".env.sample" && name != ".env.template" && name != ".env.defaults"
	}
	for _, p := range []string{".npmrc", ".pypirc", ".netrc", "credentials", "secrets", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"} {
		if name == p || strings.HasPrefix(name, p+".") {
			return true
		}
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}
func copyClipboard(data []byte) error {
	for _, name := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		var cmd *exec.Cmd
		switch name {
		case "xclip":
			cmd = exec.Command(path, "-selection", "clipboard")
		case "xsel":
			cmd = exec.Command(path, "--clipboard", "--input")
		default:
			cmd = exec.Command(path)
		}
		cmd.Stdin = bytes.NewReader(data)
		return cmd.Run()
	}
	return fmt.Errorf("no supported clipboard command found (pbcopy, wl-copy, xclip, xsel)")
}
func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
func must(err error) {
	if err != nil {
		die("%v", err)
	}
}
