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

> 请务必知悉 `grf attach` 会暂停程序直至命令退出。

打开的 HTML 页面会显示堆内存的引用分布。你可以选择查看 "inuse space" 或 "inuse objects"。

例如，从一个 [测试程序](https://github.com/cloudwego/goref/blob/main/test/testdata/mockleak/main.go) 中采集的堆内存引用分布如下所示，它反映了对象创建的调用栈分布。

![img_v3_02gq_63631612-6f2d-40ce-8f98-a4e52682ef7g](https://github.com/user-attachments/assets/9fb6bded-3f68-4b73-972d-a273c45b7680)


使用 goref 工具，你可以看到 GC 可达的堆对象的内存引用分布，从而快速定位持有引用的实际代码位置。

![img_v3_02gq_53dc2cff-203a-4c06-9678-aae49da4754g](https://github.com/user-attachments/assets/7a6b5a83-e3cd-415f-a5c0-c88d6493e45b)

![img_v3_02gq_54551396-a4ae-42b8-996f-1b1699d381dg](https://github.com/user-attachments/assets/2466c26a-eb78-4be9-af48-7a25e851982a)

它还支持分析 core 文件，例如：

```
$ grf core ${execfile} ${corefile}
successfully output to `grf.out`
```

## Go 版本约束

- 可执行文件: go1.19 ~ go1.25
- 编译 goref 工具: >= go1.21


## 文档

[实现原理](docs/principle_cn.md) | [高级用法](docs/advanced_usage_cn.md)

## 鸣谢

感谢 [Delve](https://github.com/go-delve/delve) 提供的优秀 go 调试器。
