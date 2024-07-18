# Pprof 真的够用吗？

作为 Go 研发经常会遇到内存泄露的情况，大部分人第一时间会尝试打一个 heap profile 看问题原因。但很多时候，heap profile 火焰图对问题排查起不到什么帮助，因为它只记录了对象是在哪创建的。然而，在一些复杂业务场景下，对象经过多层依赖传递或者内存池复用，几乎已经无法根据创建的堆栈信息定位根因。

众所周知， Go 是带 GC 的语言，一个对象无法释放，几乎 100% 是由于 GC 通过引用分析将其标记为存活。而同样作为 GC 语言，Java 的分析工具就更加完善了，比如 JProfiler 可以有效地给出对象引用关系。因此，我们也想在 Go 上实现一个高效的引用分析工具，能够准确直接地告诉我们内存引用分布和引用关系，帮我们从艰难的静态分析中解放出来。

好消息是，我们已基本完成了这个工具的开发工作，使用方式见 README 文档。以下将对这个工具的实现做详细讲解。

# 思路

## GC 标记过程

在讲具体实现之前，我们先回顾一下 GC 是怎么标记对象的存活的。

Go 采用类似于 tcmalloc 的分级分配方案，每个堆对象在分配时会指定到一个`mspan`上，它的size是固定的。在GC时，一个堆地址会根据多级索引查找到这个`mspan`，从而得到原始对象的base和size。在 gc bitmap 中标记了一个对象所在内存的每 8 字节对齐的地址是否是一个指针类型，从而判断是否进一步标记下游对象。

例如以下 go 代码片段：

```Go
type Object struct {
    A string
    B int64
    C *[]byte
}
// global variables
var a = echo()
var b *int64 = &echo().B
func echo() *Object {
    bytes := make([]byte, 1024)
    return &Object{A: string(bytes), C: &bytes}
}
```

GC 在扫描变量`b`时，不是只简单地扫描`B int64`这个字段的内存，而是通过`mspan`索引查找出`base`和`elem size`后再进行扫描，因此，字段 A 和 C 以及它们的下游对象的内存都会被标记为存活。

GC 扫描变量`a`变量时，发现对应的gc bit是`1010`，怎么理解呢？可以认为是`base+8`和`base+24`的地址是指针，要继续扫描下游对象，这里`A string`和`C *[]byte`都包含了一个指向下游对象的指针。

基于以上的简要分析，我们可以发现，要找到所有存活的对象，简单的原理就是从 GC Root 出发，挨个扫描对象的 gc bit，如果某个地址被标记为`1`，就继续向下扫描，每个下游地址都要确定它的 mspan，从而获取完整的对象基地址、大小和 gc bit。

## DWARF 类型信息

然而，光知道对象的引用关系对于问题排查几乎没有任何帮助。因为它不能输出任何有效的可供研发定位问题的变量名称。所以，还有一个很关键的步骤是，获取到这些对象的变量名和类型信息。

Go 本身是静态语言，对象一般不直接包含其类型信息，比如我们通过`obj=new(Object)`函数创建一个对象，实际内存只存储了`A/B/C`三个字段的值，在内存中只有 32 字节大小。既然如此，有什么办法能拿到类型信息呢？

# Goref 的实现

## Delve工具介绍

有过 Go 开发经历的同学应该都用过 Delve，如果你觉得自己没用过，不要怀疑，你在 Goland IDE 上玩的代码调试功能，底层就是基于 Delve 的。说到这里，相信大家已经回忆起 Debug 时调试窗口的画面了，没错，调试窗口所展示的变量名，变量值，变量类型这些信息，不正是我们需要的类型信息吗！

那么，Delve 是怎么获取这些变量类型信息的呢？在我们 attach 进程时，delve 从`/proc/<pid>/exe`读取软链接到实际 elf 文件路径的可执行文件。Go 编译时会生成一些调试信息，以 DWARF 标准格式存储在可执行文件的 `.debug_*` 前缀的 section 节里。引用分析所需要的全局变量和局部变量的类型信息就可以通过这些 DWARF 信息解析得到。

对于全局变量：Delve 迭代读取所有 DWARF Entry ，解析出带`Variable`标签的全局变量的 DWARF Entry。这些 Entry 包含了 Location、Type、Name 等属性。

1. 其中，Type 属性记录了它的类型信息，按 DWARF 格式递归遍历，可以进一步确定变量的每一个子对象类型；

2. Location 则是一个相对复杂的属性，它记录了一个可执行的表达式或者一个简单的变量地址，作用是确定一个变量的内存地址，或者返回寄存器的值。在全局变量解析时，Delve 通过它获得了变量的内存地址。


Goroutine 中的局部变量解析的原理与全局变量大同小异，不过还是要更复杂一些。比如需要根据 PC 确定 DWARF offset，同时 location 表达式也会更复杂，还涉及到寄存器访问。这里不再展开。

## GC 分析的元信息构建

通过 Delve 提供的进程 attach 和 core 文件分析功能，我们还可以获取到内存访问权限。我们仿照 GC 标记对象的做法，在工具的运行时内存中构建待分析进程的必要元信息。这包括：

1. 待分析进程的各个 goroutine stack 的地址空间范围，并包括每个 goroutine stack 存储 gcmask 的 `stackmap`，用来标记是否可能指向一个存活的堆对象；

2. 待分析进程的各个 data/bss segment 的地址空间范围，包括每个 segment 的 gcmask，也是用来标记是否可能指向一个存活的堆对象；

3. 以上两步都是获取 GC Roots 的必要信息；

4. 最后一步是读取待分析进程的 `mspan` 索引，以及每个 `mspan` 的 base、elem size、gcmask等信息，在工具的内存中复原这个索引；


以上步骤是大概的流程，其中还有一些细节问题的处理，例如对 gc finalizer 对象的处理，以及对 go1.22 版本 allocation header 特性的特殊处理，这里不再展开。

## DWARF 类型扫描

万事俱备，只欠东风。不管是堆扫描的 GC 元信息，还是 GC Root 变量的类型信息都已经完成解析。那么所谓的“东风”就是最关键的对象引用关系分析环节了。

我们调用`findRef`函数，按不同的 DWARF 类型访问对象的内存，假设是一个可能指向下游对象的指针，则读取指针的值，在 GC 元信息里找到这个下游对象。这时，按前所述，我们得到了对象的 base、elem size、gcmask 等信息。

如果对象被访问到，记录一个 mark bit 位，以避免对象被重复访问。通过 DWARF 子对象类型构造一个新的变量，再次递归调用`findRef`直至所有已知类型的对象被全部确认。

然而，这种引用扫描方式和 GC 的做法是完全相悖的。主要原因在于，Go 里面有大量不安全的类型转换，可能某个对象在创建后是带了指针字段的对象，比如：

```Go
func echo() *byte {
    bytes := make([]byte, 1024)
    obj := &Object{A: string(bytes), C: &bytes}
    return (*byte)(unsafe.Pointer(obj))
}
```

从 GC 的角度出发，虽然 unsafe 转换了类型为`*byte`，但并没有影响其 gcmask 的标记，所以在扫描下游对象时，仍然能扫描到完整的`Object`对象，识别到`bytes`这个下游对象，从而将其标记为存活。

但 DWARF 类型扫描可做不到，在扫描到 `byte` 类型时，会被认为是无指针的对象，直接跳过进一步的扫描了。所以，唯一的办法是，优先以 DWARF 类型扫描，对于无法扫到的对象，再用 gc 的方式来标记。

要实现这一点，做法是每当我们用 DWARF 类型访问一个对象的指针时，都将其对应的 gcmask 从 1 标记为 0，这样在扫描完一个对象后，如果对象的地址空间范围内仍然有非 0 标记的指针，就把它记录到最终标记的任务里。等到所有对象通过 DWARF 类型扫描完成后，再把这些最终标记任务取出来，以 GC 的做法二次扫描。

例如，上述 `Object` 对象访问时，其 gcmask 是`1010`，读取字段 A 后，gcmask 变成 `1000`，如果字段 C 因为类型强转或内存越界没有访问到，则在最终扫描的 GC 标记时就会被统计到。

## 最终扫描

上述的 C 字段，或者因为超过了 DWARF 定义的地址范围而无法访问到的字段，又或者像 `unsafe.Pointer` 这种无法确定类型的变量，都会在最终扫描时被标记。因为这些对象没法确定具体的类型，所以不需要专门输出，只需要把 size 和 count 记录到已知的引用链路中即可。

在 go 原生实现中，有不少常用库都采用了`unsafe.Pointer`，导致子对象识别出现问题，这类类型要做特殊处理。

## 输出文件格式

所有对象扫描完毕后，将引用链路及其对象数、对象内存空间输出到文件，文件对齐 pprof 二进制文件格式，采用 protobuf 编码。

1. **输出的根对象格式：**

- 栈变量格式：包名 + 函数名 + 栈变量名

    `github.com/cloudwego/kitex/client.invokeHandleEndpoint.func1.sendMsg`

- 全局变量格式：包名 + 全局变量名

    `github.com/cloudwego/kitex/``pkg/loadbalance/lbcache.balancerFactories`

2. **输出的子对象格式：**

- 输出子对象的字段名和类型名，形如：

    `Conn. (net.Conn)`

# 效果展示

以下是一个真实业务用工具采样后的对象引用火焰图：
![](https://bytedance.larkoffice.com/space/api/box/stream/download/asynccode/?code=ODM4YmExZWYzMmFiMTNiMzQ2ZGE5YTc0ZjE4ZGRmYjdfYUlFa0pQRjRpU2hjZnp6UElLb09SYWFpbXFUbjViYXhfVG9rZW46QVRMaWJ4UXo0b2owaHl4dTUwUGNhOXA1bmVnXzE3MjEzMDM1NDk6MTcyMTMwNzE0OV9WNA)

图中展示了每个 root 变量的名称，以及其引用的字段名和类型名，从中可以看到，内存 cache 占了较大空间。

选择 inuse\_objects 标签，还可以查看对象数分布火焰图：
![](https://bytedance.larkoffice.com/space/api/box/stream/download/asynccode/?code=NmU2MWQ2MTllZTgyNDU3MjE4OTI1ZmYyZTk4ZWI4ZTRfck51b0FtNlN1Y08xU0VLalM2cmY5T0VWVjk3c2hVM0dfVG9rZW46RkJ3eGIwQ3Nab3lBbUZ4eVkzMGN2ZUFIbjljXzE3MjEzMDM1NDk6MTcyMTMwNzE0OV9WNA)