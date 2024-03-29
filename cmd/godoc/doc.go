// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*

Godoc extracts and generates documentation for Go programs.

It has two modes.

Without the -http flag, it runs in command-line mode and prints plain text
documentation to standard output and exits. If the -src flag is specified,
godoc prints the exported interface of a package in Go source form, or the
implementation of a specific exported language entity:

	godoc fmt                # documentation for package fmt
	godoc fmt Printf         # documentation for fmt.Printf
	godoc -src fmt           # fmt package interface in Go source form
	godoc -src fmt Printf    # implementation of fmt.Printf

In command-line mode, the -q flag enables search queries against a godoc running
as a webserver. If no explicit server address is specified with the -server flag,
godoc first tries localhost:6060 and then http://golang.org.

	godoc -q Reader Writer
	godoc -q math.Sin
	godoc -server=:6060 -q sin

With the -http flag, it runs as a web server and presents the documentation as a
web page.

	godoc -http=:6060

Usage:
	godoc [flag] package [name ...]

The flags are:
	-v
		verbose mode
	-q
		arguments are considered search queries: a legal query is a
		single identifier (such as ToLower) or a qualified identifier
		(such as math.Sin).
	-src
		print (exported) source in command-line mode
	-tabwidth=4
		width of tabs in units of spaces
	-timestamps=true
		show timestamps with directory listings
	-index
		enable identifier and full text search index
		(no search box is shown if -index is not set)
	-index_files=""
		glob pattern specifying index files; if not empty,
		the index is read from these files in sorted order
	-index_throttle=0.75
		index throttle value; a value of 0 means no time is allocated
		to the indexer (the indexer will never finish), a value of 1.0
		means that index creation is running at full throttle (other
		goroutines may get no time while the index is built)
	-write_index=false
		write index to a file; the file name must be specified with
		-index_files
	-maxresults=10000
		maximum number of full text search results shown
		(no full text index is built if maxresults <= 0)
	-path=""
		additional package directories (colon-separated)
	-html
		print HTML in command-line mode
	-goroot=$GOROOT
		Go root directory
	-http=addr
		HTTP service address (e.g., '127.0.0.1:6060' or just ':6060')
	-server=addr
		webserver address for command line searches
	-sync="command"
		if this and -sync_minutes are set, run the argument as a
		command every sync_minutes; it is intended to update the
		repository holding the source files.
	-sync_minutes=0
		sync interval in minutes; sync is disabled if <= 0
	-templates=""
		directory containing alternate template files; if set,
		the directory may provide alternative template files
		for the files in $GOROOT/lib/godoc
	-filter=""
		filter file containing permitted package directory paths
	-filter_minutes=0
		filter file update interval in minutes; update is disabled if <= 0
	-zip=""
		zip file providing the file system to serve; disabled if empty

The -path flag accepts a list of colon-separated paths; unrooted paths are relative
to the current working directory. Each path is considered as an additional root for
packages in order of appearance. The last (absolute) path element is the prefix for
the package path. For instance, given the flag value:

	path=".:/home/bar:/public"

for a godoc started in /home/user/godoc, absolute paths are mapped to package paths
as follows:

	/home/user/godoc/x -> godoc/x
	/home/bar/x        -> bar/x
	/public/x          -> public/x

Paths provided via -path may point to very large file systems that contain
non-Go files. Creating the subtree of directories with Go packages may take
a long amount of time. A file containing newline-separated directory paths
may be provided with the -filter flag; if it exists, only directories
on those paths are considered. If -filter_minutes is set, the filter_file is
updated regularly by walking the entire directory tree.

When godoc runs as a web server and -index is set, a search index is maintained.
The index is created at startup and is automatically updated every time the
-sync command terminates with exit status 0, indicating that files have changed.

If the sync exit status is 1, godoc assumes that it succeeded without errors
but that no files changed; the index is not updated in this case.

In all other cases, sync is assumed to have failed and godoc backs off running
sync exponentially (up to 1 day). As soon as sync succeeds again (exit status 0
or 1), the normal sync rhythm is re-established.

The index contains both identifier and full text search information (searchable
via regular expressions). The maximum number of full text search results shown
can be set with the -maxresults flag; if set to 0, no full text results are
shown, and only an identifier index but no full text search index is created.

The presentation mode of web pages served by godoc can be controlled with the
"m" URL parameter; it accepts a comma-separated list of flag names as value:

	all	show documentation for all (not just exported) declarations
	src	show the original source code rather then the extracted documentation
	text	present the page in textual (command-line) form rather than HTML
	flat	present flat (not indented) directory listings using full paths

For instance, http://golang.org/pkg/math/big/?m=all,text shows the documentation
for all (not just the exported) declarations of package big, in textual form (as
it would appear when using godoc from the command line: "godoc -src math/big .*").

By default, godoc serves files from the file system of the underlying OS.
Instead, a .zip file may be provided via the -zip flag, which contains
the file system to serve. The file paths stored in the .zip file must use
slash ('/') as path separator; and they must be unrooted. $GOROOT (or -goroot)
must be set to the .zip file directory path containing the Go root directory.
For instance, for a .zip file created by the command:

	zip go.zip $HOME/go

one may run godoc as follows:

	godoc -http=:6060 -zip=go.zip -goroot=$HOME/go

See "Godoc: documenting Go code" for how to write good comments for godoc:
http://blog.golang.org/2011/03/godoc-documenting-go-code.html
*/
package documentation
