# Goref

[![WebSite](https://img.shields.io/website?up_message=cloudwego&url=https%3A%2F%2Fwww.cloudwego.io%2F)](https://www.cloudwego.io/)
[![License](https://img.shields.io/github/license/cloudwego/goref)](https://github.com/cloudwego/goref/blob/main/LICENSE-APACHE)

Goref is a Go heap object reference analysis tool based on delve.

It can display the space and object count distribution of Go memory references, which is helpful for efficiently locating memory leak issues or viewing persistent heap objects to optimize GC overhead.

## Installation

```
$ go install github.com/cloudwego/goref/cmd/grf@latest
```

## Usage

Attach to a running process with its PID, and then use go pprof tool to open the output file.

```
$ grf attach ${PID}
successfully output to `grf.out`
$ go tool pprof -http=:5079 ./grf.out
```

The opened HTML page displays the reference distribution of the heap memory. You can choose to view the "inuse space" or "inuse objects".

For example, the following picture is a Heap Profile of pprof. It can be seen that the objects are mainly allocated by `FastRead` function, which is Kitex's deserialization code. This flame graph is not very helpful for troubleshooting because memory allocation for decoding and constructing data is normal.

![image](https://github.com/user-attachments/assets/799c1b9a-fcf0-4b35-ab15-03a2bf3a919e)

However, by using the goref tool, the following results can be seen: `mockCache` holding onto RPC's Response causing memory not to be released, making the problem clear at a glance.

![image](https://github.com/user-attachments/assets/1c3d5d29-953d-4364-84b9-69ae35f51152)



It also supports analyzing core files, e.g.

```
$ grf core ${execfile} ${corefile}
successfully output to `grf.out`
```

## Go Version Constraints

- Executable file: go1.17 ~ go1.23.
- Compile goref tool: >= go1.21.


## Docs

[How it Works](docs/principle.md) | [实现原理](docs/principle_cn.md)

## Credit

Thanks to [Delve](https://github.com/go-delve/delve) for providing powerful golang debugger.
