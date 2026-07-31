package docs

import (
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// ResourcePage is where a kind's page lives by default: beside its handler, at
// "resources/<singular>/<singular>.md".
//
// A provider whose kinds are grouped — four package kinds under one family
// directory, say — cannot derive that from the singular, so it says where its
// pages are with ProviderDocs.PagePath instead. This is only the default.
func ResourcePage(singular string) string {
	return path.Join("resources", singular, singular+".md")
}

// Tree builds a documentation filesystem from pages that live next to their
// code.
//
// A provider whose kinds are each their own Go package cannot embed one tree:
// go:embed only reaches inside the package it is written in. So each package
// embeds its own page and the provider assembles them here, into the layout the
// site and the generator both expect.
func Tree(pages map[string]string) fs.FS {
	files := make(map[string]string, len(pages))
	for name, content := range pages {
		files[path.Clean(name)] = content
	}
	return treeFS(files)
}

type treeFS map[string]string

func (t treeFS) Open(name string) (fs.File, error) {
	if content, ok := t[path.Clean(name)]; ok {
		return &treeFile{name: path.Base(name), content: content}, nil
	}
	if t.isDir(name) {
		return &treeDir{fs: t, name: name}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (t treeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	prefix := path.Clean(name)
	if prefix == "." {
		prefix = ""
	} else {
		prefix += "/"
	}

	seen := map[string]bool{}
	var out []fs.DirEntry
	for file := range t {
		rest, ok := strings.CutPrefix(file, prefix)
		if !ok || rest == "" {
			continue
		}
		head, _, isDir := strings.Cut(rest, "/")
		if seen[head] {
			continue
		}
		seen[head] = true
		out = append(out, treeEntry{name: head, dir: isDir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (t treeFS) isDir(name string) bool {
	prefix := path.Clean(name) + "/"
	if name == "." {
		return true
	}
	for file := range t {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}

type treeFile struct {
	name    string
	content string
	offset  int
}

func (f *treeFile) Stat() (fs.FileInfo, error) {
	return treeInfo{name: f.name, size: int64(len(f.content))}, nil
}

func (f *treeFile) Read(b []byte) (int, error) {
	if f.offset >= len(f.content) {
		return 0, io.EOF
	}
	n := copy(b, f.content[f.offset:])
	f.offset += n
	return n, nil
}

func (f *treeFile) Close() error { return nil }

type treeDir struct {
	fs   treeFS
	name string
}

func (d *treeDir) Stat() (fs.FileInfo, error) {
	return treeInfo{name: path.Base(d.name), dir: true}, nil
}

func (d *treeDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: fs.ErrInvalid}
}

func (d *treeDir) Close() error { return nil }

func (d *treeDir) ReadDir(int) ([]fs.DirEntry, error) { return d.fs.ReadDir(d.name) }

type treeEntry struct {
	name string
	dir  bool
}

func (e treeEntry) Name() string      { return e.name }
func (e treeEntry) IsDir() bool       { return e.dir }
func (e treeEntry) Type() fs.FileMode { return e.mode() & fs.ModeType }
func (e treeEntry) Info() (fs.FileInfo, error) {
	return treeInfo{name: e.name, dir: e.dir}, nil
}
func (e treeEntry) mode() fs.FileMode {
	if e.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

type treeInfo struct {
	name string
	size int64
	dir  bool
}

func (i treeInfo) Name() string { return i.name }
func (i treeInfo) Size() int64  { return i.size }
func (i treeInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i treeInfo) ModTime() time.Time { return time.Time{} }
func (i treeInfo) IsDir() bool        { return i.dir }
func (i treeInfo) Sys() any           { return nil }
