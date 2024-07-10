# Goref

[![WebSite](https://img.shields.io/website?up_message=cloudwego&url=https%3A%2F%2Fwww.cloudwego.io%2F)](https://www.cloudwego.io/)
[![License](https://img.shields.io/github/license/cloudwego/goref)](https://github.com/cloudwego/goref/blob/main/LICENSE-APACHE)

Goref is a Go heap object reference analysis tool based on delve.
It can display the space and object count distribution of Go memory references, which is helpful for efficiently locating memory leak issues or viewing persistent heap objects to optimize GC overhead.

## Installation

Clone the git repository and build:

```
$ git clone https://github.com/cloudwego/goref
$ cd goref
$ go install github.com/cloudwego/goref/cmd/grf
```

> Supported go version to compile the command tool: go1.21 ~ go1.22.

## Usage

Attach to a running process with its PID, and then use go pprof tool to open the output file.

```
$ grf attach ${PID}
successfully output to `grf.out`
$ go tool pprof -http=:5079 ./grf.out
```

The opened HTML page displays the reference distribution of the heap memory. You can choose to view the "inuse space" or "inuse objects".

It also supports analyzing core files, e.g.

```
$ grf core ${execfile} ${corefile}
successfully output to `grf.out`
```

> Supported go version for executable file: go1.17 ~ go1.22.

## Credit

Thanks to [Delve](https://github.com/go-delve/delve) for providing powerful golang debugger.