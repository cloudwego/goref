# Goref

[![WebSite](https://img.shields.io/website?up_message=cloudwego&url=https%3A%2F%2Fwww.cloudwego.io%2F)](https://www.cloudwego.io/)
[![License](https://img.shields.io/github/license/cloudwego/goref)](https://github.com/cloudwego/goref/blob/main/LICENSE-APACHE)

Goref is a Go heap object reference analysis tool based on delve.

It can display the space and object count distribution of Go memory references, which is helpful for efficiently locating memory leak issues or viewing persistent heap objects to optimize the garbage collector (GC) overhead.

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

For example, the heap profile sampled from a Kitex RPC service is shown below, which reflects the call stack distribution of object creation. The largest proportion, FastRead, is the function for object deserialization.

![image](https://github.com/user-attachments/assets/495d0884-332a-4570-b41b-3019e4d9b3c1)



By using the goref tool, you can see the memory reference distribution of heap objects reachable by GC, thereby quickly pinpointing the actual code locations holding references.

![image](https://github.com/user-attachments/assets/9f547dc8-6b40-439b-9aee-6c5de8dfdee5)






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
