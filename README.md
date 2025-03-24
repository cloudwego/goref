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

For example, the heap profile sampled from a [testing program](https://github.com/cloudwego/goref/blob/main/testdata/mockleak/main.go) is shown below, which reflects the call stack distribution of object creation.

![img_v3_02gq_63631612-6f2d-40ce-8f98-a4e52682ef7g](https://github.com/user-attachments/assets/9fb6bded-3f68-4b73-972d-a273c45b7680)

By using the goref tool, you can see the memory reference distribution of heap objects reachable by GC, thereby quickly pinpointing the actual code locations holding references.

![img_v3_02gq_53dc2cff-203a-4c06-9678-aae49da4754g](https://github.com/user-attachments/assets/7a6b5a83-e3cd-415f-a5c0-c88d6493e45b)

![img_v3_02gq_54551396-a4ae-42b8-996f-1b1699d381dg](https://github.com/user-attachments/assets/2466c26a-eb78-4be9-af48-7a25e851982a)

It also supports analyzing core files, e.g.

```
$ grf core ${execfile} ${corefile}
successfully output to `grf.out`
```

## Go Version Constraints

- Executable file: go1.17 ~ go1.24.
- Compile goref tool: >= go1.21.


## Docs

[How it Works](docs/principle.md) | [实现原理](docs/principle_cn.md)

## Credit

Thanks to [Delve](https://github.com/go-delve/delve) for providing powerful golang debugger.
