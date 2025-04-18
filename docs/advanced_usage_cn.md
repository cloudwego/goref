# 高阶用法

## 设置引用链路深度

Goref 默认的引用分析最大深度是256，这是为了能最大限度展示对象的引用关系图，但是在某些情况下，可能会导致分析时间过长。因此，我们提供了一个命令行参数`--max-depth`，可以设置引用分析的最大深度。

例如，Go程序很常见的是 `context.Context` 深度嵌套，但是我们只需要分析到 `context.Context` 的前几层，那么可以使用以下命令：

```bash
goref attach ${pid} --max-depth=10
```

对于引用链路过深的场景，设置这个参数可以帮助我们加速 goref 运行。

## 采集coredump文件分析

Goref 以 attach 模式运行会占用额外内存，在内存泄漏场景，可能导致 OOM 问题。因此，用户可使用 `gcore` 命令采集 coredump 文件，将进程可执行文件一并压缩后拷贝到其它环境，然后使用 `grf core` 命令进行分析。

以 Debian 11 为例：
```bash
$ 
$ apt-get update
$ apt-get install gdb
$ gcore ${pid}
$ ...
$ grf core ${execfile} ${corefile}
```
