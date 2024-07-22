# Is Pprof Really Sufficient?

As a Go developer, we may encounter situations of memory leaks from time to time. Most people would attempt to take a heap profile as their first step to identify the cause of the problem. However, in many cases, the heap profile flame graph is not very helpful for troubleshooting because it only records where objects are created. In complex business scenarios where objects are passed through multiple layers of dependencies or memory pools, it becomes almost impossible to locate the root cause based solely on the stack information of object creation.

Take the following heap profile as an example. The stack of the FastRead function is a deserialization function in the Kitex framework. If a business goroutine leaks a request object, it actually cannot reflect the corresponding leaked code position but only shows that the FastRead function stack occupies memory.

![image](https://github.com/user-attachments/assets/462ee4da-02d0-465c-90ee-c1c59fc86dc9)

As we all know, Go is a language with a garbage collector (GC). If an object cannot be released, it is almost 100% due to the fact that the GC marks it as alive through reference analysis. As a GC language, Java's analysis tools are more advanced. For example, JProfiler can effectively display object reference relationships. Therefore, we also want to develop an efficient reference analysis tool for Go that can accurately and directly show us memory reference distribution and reference relationships, liberating us from difficult static analysis.

The good news is that we have basically completed the development of this tool. Please refer to the README document for instructions on how to use it and see its effectiveness. The following will discuss the design concept and detailed implementation of this tool.

# Ideas

## GC Mark Process

Before diving into the specific implementation, let's review how GC marks objects as alive.

Go adopts a tiered allocation scheme similar to tcmalloc. Each heap object is assigned to an `mspan` when allocated, and its size is fixed. During GC, a heap address calls `runtime.spanOf` to look up the corresponding `mspan` from a multi-level index, obtaining the base address and size of the original object.

```Go
// simplified code
func spanOf(p uintptr) *mspan {
    ri := arenaIndex(p)
    ha := mheap_.arenas[ri.l1()][ri.l2()]
    return ha.spans[(p/pageSize)%pagesPerArena]
}
```

By using the `runtime.heapBitsForAddr` function, we can obtain the GC bitmap for a range of object addresses. The GC bitmap marks whether each 8-byte aligned address within the memory of an object is a pointer type, thus determining whether to further mark downstream objects.

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

When the GC scans variable `b`, it doesn't simply scan the memory of the field `B int64`. Instead, it looks up the base and elem size through the `mspan` index and then performs the scan. Therefore, the memory of fields A and C, as well as their downstream objects, will be marked as live.

When the GC scans variable `a`, it encounters the GC bit sequence `1001`. How do we interpret this? We can interpret it as indicating that the addresses `base+0` and `base+24` are pointers and need to be further scanned for downstream objects. Here, both `A string` and `C *[]byte` contain pointers to downstream objects.

<p align="center"><img src="https://github.com/user-attachments/assets/1fb72f61-d593-4f45-b9b4-61eadd24c063" width="60%"></p>

Based on the brief analysis above, we can observe that to find all live objects, the fundamental principle is to start from the GC Roots and scan the GC bits of each object. If an address is marked as `1`, continue scanning downstream. For each downstream address, it's necessary to determine its `mspan` to obtain the complete object's base address, size, and GC bit information.

## DWARF Type Information

However, knowing the object's reference relationships alone is almost of no help for troubleshooting because it doesn't provide any useful variable names that developers can use to locate the problem. Therefore, there is a crucial step to retrieve the variable names and type information of these objects.

Go itself is a statically-typed language, and objects generally do not directly contain their type information. For example, when we create an object using `obj = new(Object)`, the actual memory only stores the values of the three fields `A/B/C`, occupying only 32 bytes of memory. In that case, how can we obtain the type information?

# Goref Implementation

## Delve Tool Introduction

Those who have experience with Go development should have used Delve. If you feel like you haven't used it, don't doubt yourself. The code debugging functionality you've used in the Goland IDE is actually based on Delve. Now that we mention it, I believe everyone has recalled the visual display in the debugging window during debugging. Yes, the variable names, variable values, and variable types shown in the debugging window are exactly the type information we need!

```
$ ./dlv attach 270
(dlv) ...
(dlv) locals
tccCli = ("*code.byted.org/gopkg/tccclient.ClientV2")(0xc000782240)
ticker = (*time.Ticker)(0xc001086be0)
```

So how does Delve obtain this variable information? When attaching to a process, Delve reads the executable file from `/proc/<pid>/exe`, which is a symbolic link to the actual ELF file path. During Go compilation, debug information is generated and stored in sections with the `.debug_*` prefix in the executable file, following the DWARF standard format. The type information for global variables and local variables, which is required for reference analysis, can be parsed from these DWARF information.

For global variables: Delve iterates through all DWARF entries and extracts the DWARF entries with the `Variable` tag, representing global variables. These entries contain attributes such as Location, Type, and Name.

1. The Type attribute records the type information of the variable. By recursively traversing the DWARF format, the type of each sub-object of the variable can be determined.

2. The Location attribute is a relatively complex attribute that records an executable expression or a simple variable address. Its purpose is to determine the memory address of a variable or return the value of a register. During the resolution of global variables, Delve uses the Location attribute to obtain the memory address of the variable.

The principle of resolving local variables in Goroutines is similar to that of global variables, but it is slightly more complex. For example, it requires determining the DWARF offset based on the PC (Program Counter), and the location expressions can be more complex, involving register access. However, we won't delve into these details here.

## Construction of Metadata for GC Analysis

Through the process attach and core file analysis features provided by Delve, we can also obtain memory access permissions. Following the approach of marking objects during GC, we construct the necessary metadata for the process being analyzed within the runtime memory of the tool. This includes:

1. The address space range of each Goroutine stack in the process being analyzed, including the `stackmap` that stores the gcmask for each Goroutine stack. This `stackmap` is used to mark whether a stack may contain references to live heap objects.

2. The address space range of each data/bss segment in the process being analyzed, including the gcmask for each segment. This gcmask is also used to mark whether a segment may contain references to live heap objects.

3. The above two steps are necessary for obtaining the GC Roots information.

4. The final step is to read the mspan index of the process being analyzed, as well as the base, elem size, gcmask, and other information for each mspan, and reconstruct this index within the tool's memory.

The above steps provide a general overview of the process, but there are still some details to address, such as handling GC finalizer objects and special handling of the allocation header feature in Go version 1.22. We won't delve into these details here.

## DWARF Type Scan

All preparations are complete except one thing. Whether it is the GC metadata for heap scanning or the type information for GC root variables, they have been successfully parsed. Now, the most crucial step of object reference analysis begins its execution.

For each GC root variable, we invoke the `findRef` function, which accesses the memory of the object based on different DWARF types. Assuming it is a pointer that may point to downstream objects, we read the value of the pointer and locate the downstream object in the GC metadata. At this point, as mentioned earlier, we obtain information such as the object's base address, element size, and gcmask.

If the object is accessed, we mark a bit to avoid redundant access to the object. We construct a new variable based on the DWARF sub-object type and recursively call `findRef` until all objects of known types are confirmed.

However, this reference scanning approach contradicts the traditional GC approach. The main reason is that there are numerous unsafe type conversions in Go. For example, an object may initially be created as an object with pointer fields, such as:

```Go
func echo() *byte {
    bytes := make([]byte, 1024)
    obj := &Object{A: string(bytes), C: &bytes}
    return (*byte)(unsafe.Pointer(obj))
}
```

From the perspective of GC, although there is an unsafe type conversion to `*byte`, it does not affect the marking of its gcmask. Therefore, when scanning downstream objects, the complete `Object` object can still be scanned, and the downstream object bytes can be recognized and marked as live.

However, DWARF type scanning cannot achieve the same result. When scanning a `byte` type, it is considered an object without pointers, and further scanning is skipped. Therefore, the only solution is to prioritize DWARF type scanning, and for objects that cannot be scanned this way, use the GC approach to mark them.

To achieve this, whenever we access a pointer of an object using DWARF types, we mark its corresponding gcmask from 1 to 0. After scanning an object, if there are still pointers with non-zero marks within the object's address space range, we record them as tasks for final marking. After completing the DWARF type scanning for all objects, we retrieve these tasks and perform a second scan following the GC approach.

<p align="center"><img src="https://github.com/user-attachments/assets/cb286079-a7bd-4ef4-9c07-21eb8eb7fd80" width="60%"></p>

For example, in the case of accessing the `Object` object mentioned above, its gcmask is `1010`. After reading field A, the gcmask becomes `1000`. If field C is not accessed due to type coercion, it will be accounted for during the final scan using the GC marking.

In addition to type coercion, referencing memory out of bounds is also a common issue. For instance, in the previous example code `var b *int64 = &echo().B`, both fields A and C belong to memory that cannot be scanned by DWARF types and will be accounted for during the final scan.

## Final Scan

Fields that have been type-coerced, cannot be accessed due to exceeding the address range defined by DWARF, or variables of undetermined type such as `unsafe.Pointer`, will be marked during the final scan. Since the specific types of these objects cannot be determined, there is no need to output them separately. Instead, their size and count are recorded in the known reference chain.

In the native Go implementation, many commonly used libraries utilize `unsafe.Pointer`, causing issues with identifying sub-objects. Special handling is required for such types.

## Output File Format

Once all objects have been scanned, the reference chains along with the number of objects and their memory space will be output to a file. The file will be aligned with the pprof binary file format and encoded using protobuf.

1. **Output root object format:**

- Stack variable format: package name + function name + stack variable name

  `github.com/cloudwego/kitex/client.invokeHandleEndpoint.func1.sendMsg`

- Global variable format: package name + global variable name

  `github.com/cloudwego/kitex/pkg/loadbalance/lbcache.balancerFactories`

2. **Output sub-object format:**

- Output the field name and type name of the child object, in the form of: `net.Conn`；

- If it is a map key or value field, it will be output in the form of `$mapkey. (type_name)` or `$mapval. (type_name)`;

- If it is an element of an array, it will be output in the format like `[0]. (type_name)`, and for indices greater than or equal to 10, it will be output in the format `[10+]. (type_name)`.

