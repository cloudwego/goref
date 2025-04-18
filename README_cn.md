# Goref

[English](README.md) | 中文

[![WebSite](https://img.shields.io/website?up_message=cloudwego&url=https%3A%2F%2Fwww.cloudwego.io%2F)](https://www.cloudwego.io/)
[![License](https://img.shields.io/github/license/cloudwego/goref)](https://github.com/cloudwego/goref/blob/main/LICENSE-APACHE)

Goref 是一个基于 Delve 的 Go 堆对象引用分析工具。

它可以显示 Go 内存引用的空间和对象数量分布，有助于高效地定位内存泄漏问题或查看持久化的堆对象以优化 GC 开销。

## 安装

```
$ go install github.com/cloudwego/goref/cmd/grf@latest
```

## 使用方式

Attach 到一个运行中的进程，然后使用 go pprof 工具打开输出文件。

```
$ grf attach ${PID}
successfully output to `grf.out`
$ go tool pprof -http=:5079 ./grf.out
```

打开的 HTML 页面会显示堆内存的引用分布。你可以选择查看 "inuse space" 或 "inuse objects"。

例如，从一个 [测试程序](https://github.com/cloudwego/goref/blob/main/testdata/mockleak/main.go) 中采集的堆内存引用分布如下所示，它反映了对象创建的调用栈分布。

![img_v3_02gq_63631612-6f2d-40ce-8f98-a4e52682ef7g](https://github.com/user-attachments/assets/9fb6bded-3f68-4b73-972d-a273c45b7680)


使用 goref 工具，你可以看到 GC 可达的堆对象的内存引用分布，从而快速定位持有引用的实际代码位置。

![img_v3_02gq_53dc2cff-203a-4c06-9678-aae49da4754g](https://github.com/user-attachments/assets/7a6b5a83-e3cd-415f-a5c0-c88d6493e45b)

![img_v3_02gq_54551396-a4ae-42b8-996f-1b1699d381dg](https://github.com/user-attachments/assets/2466c26a-eb78-4be9-af48-7a25e851982a)

它还支持分析 core 文件，例如：

```
$ grf core ${execfile} ${corefile}
successfully output to `grf.out`
```

### 高阶用法

- **设置引用链路深度**

Goref 默认的引用分析最大深度是256，这是为了能最大限度展示对象的引用关系图，但是在某些情况下，可能会导致分析时间过长。因此，我们提供了一个命令行参数`--max-depth`，可以设置引用分析的最大深度。

例如，Go程序很常见的是 `context.Context` 深度嵌套，但是我们只需要分析到 `context.Context` 的前几层，那么可以使用以下命令：

```bash
goref attach ${pid} --max-depth=10
```

对于引用链路过深的场景，设置这个参数可以帮助我们加速 goref 运行。

- **采集coredump文件分析**

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

## Go 版本约束

- 可执行文件: go1.17 ~ go1.24.
- 编译 goref 工具: >= go1.21.


## 文档

[实现原理](docs/principle_cn.md)

## 鸣谢

感谢 [Delve](https://github.com/go-delve/delve) 提供的优秀 go 调试器。
