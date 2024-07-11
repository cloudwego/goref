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


<img width="1920" alt="image" src="https://github.com/cloudwego/goref/assets/24311963/a9fe0294-fe58-456a-a9d5-a8cb25049bff">
<br /> <br />

View flame graph of inuse objects:


<img width="1916" alt="image" src="https://github.com/cloudwego/goref/assets/24311963/24e80f51-3af3-4405-8f71-57e51c42c7ed">


It also supports analyzing core files, e.g.

```
$ grf core ${execfile} ${corefile}
successfully output to `grf.out`
```

> Supported go version for executable file: go1.17 ~ go1.22.


## Principle
The main steps of Goref reference analysis are as follows:

1. Based on Delve, implement the functionality to attach processes and parse core files to achieve memory reading of the process to be analyzed.
2. Parse the goroutine stack, data/bss segments, and heap span address space ranges, as well as gcmask, from the memory of the process to be analyzed, and build an index in the tool's runtime memory.
3. Read DWARF Entries from the process executable file, parse the type descriptions of global variables and goroutine local variables, and calculate the actual memory addresses using Location expressions.
4. Starting from the root objects obtained from step 3, prioritize object retrieval based on their DWARF types, determining the reference paths of all objects whose types can be determined.
5. Search for gcmask and perform a second search for any objects that were not accessed during the DWARF retrieval in step 4, recording the found objects on the reference paths of known types.
6. Record the number of objects and memory space occupied by all reference chains in the pprof file buffer, and then flush it to a file. In this way, we'll obtain a complete flame graph of the reference chains.

## Credit

Thanks to [Delve](https://github.com/go-delve/delve) for providing powerful golang debugger.
