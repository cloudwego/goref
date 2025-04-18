# Advanced Usage

## Set the maximum depth of reference chain

Goref defaults to a maximum depth of 256 for reference analysis, which is to maximize the display of object reference graphs. However, in some cases, it may take a long time to analyze the reference chain. Therefore, we provide a command line parameter `--max-depth` to set the maximum depth of reference analysis.

For example, the common scenario of `context.Context` nesting in Go programs is that it is deeply nested, but we only need to analyze up to the first few layers of `context.Context`. Then you can use the following command:

```bash
goref attach ${pid} --max-depth=10
```

For reference chains that are too deep, setting this parameter can help us accelerate the execution of goref.

## Analyze coredump files

Goref consumes additional memory if runs in attach mode. In the memory leak scenario, it may cause OOM problems. Therefore, users can use the `gcore` command to collect coredump files, compress the executable file along with it, and then copy it to another environment for analysis.

For example, on Debian 11:

```bash
$ 
$ apt-get update
$ apt-get install gdb
$ gcore ${pid}
$ ...
$ grf core ${execfile} ${corefile}
```