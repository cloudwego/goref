# Are Pprof Profiles Really Enough?

As Go developers, we inevitably encounter memory leaks from time to time. The typical first step is to generate a heap profile to identify the root cause. However, in many cases, heap profile flame graphs offer limited diagnostic value because they only show where objects are created—not what's keeping them alive. In complex business scenarios where objects traverse multiple dependency layers or are reused through memory pools, tracing the leak back to its source becomes nearly impossible when relying solely on object creation stack traces.

Consider the following heap profile example. The FastRead function stack represents a deserialization function in the Kitex framework. When a business goroutine leaks a request object, the profile doesn't reveal the actual leaking code location—it merely shows that the FastRead function stack is consuming memory.

![image](https://github.com/user-attachments/assets/462ee4da-02d0-465c-90ee-c1c59fc86dc9)

This motivated us to develop an efficient reference analysis tool for Go that can accurately and directly show memory reference distribution and relationships, freeing developers from tedious static analysis. The good news is that we've largely completed this tool's development. For usage instructions and demonstrations of its effectiveness, please refer to the README documentation. The following sections will cover the design philosophy and detailed implementation of this tool.

# Design Philosophy

## GC Marking Process

Before diving into the implementation details, let's review how the GC marks objects as live.

Go employs a tiered allocation scheme similar to tcmalloc. Each heap object is assigned to an `mspan` upon allocation, with a fixed size for that span. During garbage collection, a heap address calls `runtime.spanOf` to look up the corresponding `mspan` from a multi-level index, thereby obtaining the base address and size of the original object.

```Go
// simplified code
func spanOf(p uintptr) *mspan {
    ri := arenaIndex(p)
    ha := mheap_.arenas[ri.l1()][ri.l2()]
    return ha.spans[(p/pageSize)%pagesPerArena]
}
```

Using the `runtime.heapBitsForAddr` function, we can obtain the GC bitmap for a range of object addresses. The GC bitmap indicates whether each 8-byte aligned address within the object's memory contains a pointer, which determines whether downstream objects should be further marked.

For example, consider the following Go code snippet:

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

When the GC scans variable `b`, it doesn't simply scan the memory of field `B int64`. Instead, it looks up the base address and element size through the `mspan` index and then performs the scan. Consequently, the memory of fields A and C, along with their downstream objects, will be marked as live.

When the GC scans variable `a`, it encounters the GC bit sequence `1001`. What does this mean? It indicates that the addresses at `base+0` and `base+24` are pointers that need further scanning for downstream objects. In this case, both `A string` and `C *[]byte` contain pointers to downstream objects.

<p align="center"><img src="https://github.com/user-attachments/assets/1fb72f61-d593-4f45-b9b4-61eadd24c063" width="60%"></p>

Based on this analysis, we can observe that to find all live objects, the fundamental principle is to start from GC Roots and systematically scan each object's GC bits. If an address is marked as `1`, we continue scanning downstream. For each downstream address, we must determine its `mspan` to obtain the complete object's base address, size, and GC bit information.

## DWARF Type Information

However, knowing object reference relationships alone provides little diagnostic value because it lacks meaningful variable names that developers need to locate issues. Therefore, a crucial step is retrieving the variable names and type information for these objects.

Go is a statically-typed language where objects typically don't include their own type metadata. For example, when we create an object using `obj = new(Object)`, the actual memory only stores the values of fields `A/B/C`, occupying just 32 bytes. So how can we obtain type information?

# Goref Implementation

## Introduction to Delve

If you have Go development experience, you've likely used Delve. Even if you think you haven't—don't be so sure. The code debugging functionality in GoLand IDE is actually built on Delve. Now that I mention it, you probably recall the visual display in the debugging window during debugging sessions. That's right—the variable names, values, and types displayed in the debugging window are exactly the type information we need!

```
$ ./dlv attach 270
(dlv) ...
(dlv) locals
tccCli = ("*code.byted.org/gopkg/tccclient.ClientV2")(0xc000782240)
ticker = (*time.Ticker)(0xc001086be0)
```

So how does Delve obtain this variable information? When attaching to a process, Delve reads the executable file from `/proc/<pid>/exe`, which is a symbolic link to the actual ELF file path. During Go compilation, debug information is generated and stored in sections with the `.debug_*` prefix in the executable file, following the DWARF standard format. The type information for global and local variables needed for reference analysis can be parsed from this DWARF information.

For global variables: Delve iterates through all DWARF entries and extracts those with the `Variable` tag, representing global variables. These entries contain attributes such as Location, Type, and Name.

1. The Type attribute records the variable's type information. By recursively traversing the DWARF format, we can determine the type of each sub-object within the variable.

2. The Location attribute is more complex—it records either an executable expression or a simple variable address. Its purpose is to determine a variable's memory address or return a register value. When resolving global variables, Delve uses the Location attribute to obtain the variable's memory address.

Resolving local variables in Goroutines follows similar principles to global variables but is slightly more complex. For example, it requires determining DWARF offsets based on the PC (Program Counter), and location expressions can be more involved, including register access. We won't dive into these details here.

## Building GC Analysis Metadata

Through Delve's process attach and core file analysis capabilities, we can also obtain memory access permissions. Following the GC's approach to object marking, we construct the necessary metadata for the target process within our tool's runtime memory. This includes:

1. The address space range of each Goroutine stack in the target process, including the `stackmap` that stores the gcmask for each Goroutine stack. This `stackmap` indicates whether a stack may contain references to live heap objects.

2. The address space range of each data/bss segment in the target process, including the gcmask for each segment. This gcmask also indicates whether a segment may contain references to live heap objects.

3. The above two steps are essential for obtaining GC Roots information.

4. The final step is reading the target process's mspan index, along with the base, element size, gcmask, and other information for each mspan, then reconstructing this index within the tool's memory.

These steps provide a general overview of the process, but there are additional details to handle, such as GC finalizer objects and special handling for the allocation header feature in Go 1.22. We won't explore these details here.

## DWARF Type Scanning

With all preparations complete, we're ready for the crucial step: object reference analysis. Both the GC metadata for heap scanning and the type information for GC root variables have been successfully parsed.

For each GC root variable, we invoke the `findRef` function, which accesses the object's memory based on different DWARF types. If we encounter a pointer that may point to downstream objects, we read the pointer value and locate the downstream object in the GC metadata. At this point, as previously mentioned, we obtain the object's base address, element size, and gcmask information.

If an object is accessed, we mark it to avoid redundant access. We construct a new variable based on the DWARF sub-object type and recursively call `findRef` until all objects of known types are confirmed.

However, this reference scanning approach conflicts with the traditional GC approach, primarily due to numerous unsafe type conversions in Go. For example, an object might be created with pointer fields but later undergo unsafe type conversion:

```Go
func echo() *byte {
    bytes := make([]byte, 1024)
    obj := &Object{A: string(bytes), C: &bytes}
    return (*byte)(unsafe.Pointer(obj))
}
```

From the GC's perspective, even though the type is unsafely converted to `*byte`, this doesn't affect its gcmask marking. Therefore, when scanning downstream objects, the complete `Object` can still be scanned, and the downstream `bytes` object can be recognized and marked as live.

However, DWARF type scanning cannot achieve the same result. When scanning a `byte` type, it's treated as a non-pointer object, and further scanning is skipped. The solution is to prioritize DWARF type scanning, then use the GC approach for objects that cannot be scanned this way.

To implement this hybrid approach, whenever we access an object's pointer using DWARF types, we clear its corresponding gcmask bit from 1 to 0. After scanning an object, if there are still pointers with non-zero marks within its address space, we record them as tasks for final marking. After completing DWARF type scanning for all objects, we retrieve these tasks and perform a second scan using the GC approach.

<p align="center"><img src="https://github.com/user-attachments/assets/cb286079-a7bd-4ef4-9c07-21eb8eb7fd80" width="60%"></p>

For example, when accessing the `Object` mentioned above, its gcmask is `1010`. After reading field A, the gcmask becomes `1000`. If field C isn't accessed due to type coercion, it will be accounted for during the final GC marking scan.

Beyond type coercion, out-of-bounds memory references are another common issue. For instance, in the earlier example code `var b *int64 = &echo().B`, both fields A and C belong to memory that cannot be scanned through DWARF types and will also be accounted for during the final scan.

## Final Scanning

Fields that have been type-coerced, cannot be accessed due to exceeding DWARF-defined address ranges, or variables of indeterminate type such as `unsafe.Pointer`, will be marked during the final scan. Since the specific types of these objects cannot be determined, they don't need to be output individually. Instead, their size and count are recorded within the known reference chain.

In Go's standard library and many common libraries, extensive use of `unsafe.Pointer` creates challenges for sub-object identification. These types require special handling.

## Output File Format

Once all objects are scanned, the reference chains along with object counts and memory usage are written to a file. The file follows the pprof binary format and is encoded using protobuf.

### 1. Root Object Format

- **Stack variables**: `package_name.function_name.stack_variable_name`

  Example: `github.com/cloudwego/kitex/client.invokeHandleEndpoint.func1.sendMsg`

- **Global variables**: `package_name.global_variable_name`

  Example: `github.com/cloudwego/kitex/pkg/loadbalance/lbcache.balancerFactories`

### 2. Sub-Object Format

- **Field access**: `field_name. (type_name)`

  Example: `conn. (net.Conn)`

- **Map elements**:
  - Keys: `$mapkey. (type_name)`
  - Values: `$mapval. (type_name)`

- **Array/slice elements**:
  - Indices < 10: `[index]. (type_name)` (e.g., `[0]. (type_name)`)
  - Indices ≥ 10: `[index+]. (type_name)` (e.g., `[10+]. (type_name)`)

- **Finalizer objects**:
  - `runtime.SetFinalizer.obj` (the target object)
  - `runtime.SetFinalizer.fn` (the finalizer function)

- **Sub-objects without DWARF type info**: `$sub_objects$`