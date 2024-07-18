# Is Pprof Really Sufficient?

As Go developers, we may often encounter issues of memory leaks, and most people's initial approach is to generate a heap profile to identify the cause of the problem. However, in many cases, the heap profile flame graph is not very helpful in troubleshooting because it only records where objects were created. In complex business scenarios where objects are passed through multiple layers of dependencies or reused in memory pools, it becomes nearly impossible to locate the root cause based solely on the stack information of object creation.

It is well known that Go is a garbage-collected language, and when an object cannot be freed, it is almost always because the GC has marked it as live through reference analysis. In contrast, Java, as another GC-enabled language, has more sophisticated analysis tools. For example, JProfiler can effectively provide object reference relationships. Therefore, we also wanted to develop an efficient reference analysis tool for Go that can accurately and directly show us memory reference distribution and relationships, freeing us from the difficulties of static analysis.

The good news is that we have made significant progress in developing this tool, and its usage and results are described in the README document. The following will provide a detailed explanation of the implementation of this tool.

# Ideas

## GC Mark Process

Before diving into the specific implementation, let's review how GC marks objects as live.

Go adopts a tiered allocation scheme similar to tcmalloc, where each heap object is assigned to an `mspan` during allocation, and its size is fixed. During GC, a heap address is used to locate the corresponding `mspan` through multiple-level indexing, enabling access to the base and size of the original object. The GC bitmap marks whether each 8-byte aligned address in the memory space of an object is a pointer type, allowing for further marking of downstream objects.

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

When GC scans the variable `b`, it doesn't just scan the memory of the field `B int64` directly. Instead, it looks up the base address and elem size through the `mspan` index before performing the scan. As a result, the memory of fields `A` and `C`, as well as their downstream objects, will be marked as live.

When GC scans the variable `a`, it encounters a corresponding GC bit of `1010`. How should we interpret this? We can consider it as the addresses `base+8` and `base+24` being pointers, indicating that further scanning of downstream objects is required. Both `A string` and `C *[]byte` contain pointers that point to downstream objects.

Based on this brief analysis, we can conclude that to find all live objects, the basic principle is to start from the GC roots and scan the GC bits of objects one by one. If an address is marked as `1`, we continue scanning downstream. For each downstream address, we need to determine its mspan to obtain the complete object's base address, size, and GC bits.

## DWARF Type Information

However, knowing the object's reference relationships alone is almost useless for troubleshooting purposes. It doesn't provide any helpful variable names that developers can use to pinpoint issues. Therefore, there is a crucial step involved in obtaining the variable names and type information of these objects.

Go itself is a statically typed language, and objects typically do not directly contain their type information. For example, when we create an object using the `obj = new(Object)` function, the actual memory only stores the values of the fields `A/B/C`, occupying only 32 bytes of memory. In this case, how can we obtain the type information?

# Implementation of Goref

## Delve Tool Introduction

Those who have experience with Go development are likely familiar with Delve. Even if you think you haven't used it directly, if you've used the code debugging functionality in the Goland IDE, it is actually based on Delve underneath. Now that we've mentioned it, I believe you can recall the debugging window with variable names, values, and types displayed. Yes, those are exactly the type information we need!

So, how does Delve obtain this type information for variables? When we attach to a process, Delve reads the executable file from the symbolic link `/proc/<pid>/exe`, which points to the actual ELF file path. During Go compilation, various debug information is generated and stored in sections prefixed with `.debug_*` in the executable file, following the DWARF standard format. The type information for global and local variables, which is needed for reference analysis, can be parsed from these DWARF information.

For global variables: Delve iterates over all DWARF entries and parses the ones with the `Variable` tag, which contain attributes such as Location, Type, and Name.

1. Among them, the Type attribute records the type information of the variable. By recursively traversing it according to the DWARF format, we can further determine the type of each sub-object of the variable.

2. The Location attribute is relatively complex. It stores an executable expression or a simple variable address. Its purpose is to determine the memory address of a variable or return the value of a register. During the resolution of global variables, Delve uses the Location attribute to obtain the memory address of the variable.


The principle of resolving local variables within a Goroutine is similar to that of global variables, but it is slightly more complex. For example, it requires determining the DWARF offset based on the PC (Program Counter), and the location expressions can be more intricate, involving register access. However, delving into these details is beyond the scope of this discussion.

## Building Metadata for GC Analysis

Through the process attach and core file analysis features provided by Delve, we can also obtain memory access permissions. Following the approach of marking objects in the GC, we construct the necessary metadata of the target process in the runtime memory of our tool. This includes:

1. The address space ranges of each Goroutine stack in the target process, including the `stackmap` that stores the gcmask for each Goroutine stack. The `stackmap` is used to determine whether it may point to a live heap object.

2. The address space ranges of each data/bss segment in the target process, including the gcmask for each segment. The gcmask is also used to determine whether it may point to a live heap object.

3. The above two steps are necessary to obtain the GC Roots information.

4. The final step is to read the mspan index of the target process and reconstruct this index in the memory of our tool, including the base, elem size, gcmask, and other information for each mspan.


The above steps provide a general overview of the process, but there are additional details to consider, such as handling GC finalizer objects and special handling for the allocation header feature in Go 1.22. However, these details are beyond the scope of this discussion.

## DWARF Type Scan

All preparations are complete except one thing. Whether it is the GC metadata for heap scanning or the type information for GC root variables, they have been successfully parsed. Now, the most crucial step of object reference analysis begins its execution.

We invoke the `findRef` function and access the memory of the object based on different DWARF types. Assuming it is a pointer that may point to a downstream object, we read the value of the pointer and search for the corresponding downstream object in the GC metadata. At this point, as mentioned earlier, we have obtained information such as the object's base address, element size, and GC mask.

If the object is accessed, record a mark bit to avoid repeated access to the object. Construct a new variable using the DWARF sub-object type, and recursively invoke `findRef` again until all known types of objects are confirmed.

However, this reference scanning approach is completely contradictory to the way GC operates. The main reason is that Go contains a significant amount of unsafe type conversions. It is possible that an object, after creation, may have pointer fields, such as:

```Go
func echo() *byte {
    bytes := make([]byte, 1024)
    obj := &Object{A: string(bytes), C: &bytes}
    return (*byte)(unsafe.Pointer(obj))
}
```

From the perspective of GC, although the type was converted to `*byte` using unsafe, it did not affect the marking of its gcmask. Therefore, when scanning downstream objects, the complete `Object` object can still be scanned, and the downstream object `bytes` can be identified and marked as live.

However, this is not achievable through DWARF type scanning. When encountering the `byte` type, it is considered an object without pointers and further scanning is skipped. Therefore, the only solution is to prioritize DWARF type scanning, and for objects that cannot be scanned using this method, resort to GC-style marking.

To achieve this, each time we access a pointer of an object using the DWARF type, we mark its corresponding gcmask from 1 to 0. After scanning an object, if there are still pointers with non-zero marks within the object's address space, they are recorded as tasks for final marking. Once all objects have been scanned using the DWARF type, these final marking tasks are retrieved and subjected to a second scan using GC's approach.

For example, in the case of the `Object` object mentioned above, its gcmask is `1010`. After reading field A, the gcmask becomes `1000`. If field C is not accessed due to type coercion or memory out-of-bounds, it will be accounted for during the final GC marking scan.


## Final Scan

The aforementioned field C, fields that cannot be accessed due to exceeding the address range defined by DWARF, or variables of types like `unsafe.Pointer` that cannot have their types determined, will all be marked during the final scan. Since the specific types of these objects cannot be determined, there is no need to output them separately. It is sufficient to record their size and count in the known reference chain.

In the native Go implementation, several commonly used libraries make use of `unsafe.Pointer`, which causes issues with identifying sub-objects. Special handling is required for such types.

## Output File Format

Once all objects have been scanned, the reference chains along with the number of objects and their memory space will be output to a file. The file will be aligned with the pprof binary file format and encoded using protobuf.

1. **Output** **root object format:**

- Stack variable format: package name + function name + stack variable name

   `github.com/cloudwego/kitex/client.invokeHandleEndpoint.func1.sendMsg`

- Global variable format: package name + global variable name

   `github.com/cloudwego/kitex/``pkg/loadbalance/lbcache.balancerFactories`

2. **Output** **sub-object format:**

- Output the field name and type name of the child object, in the form of:

   `Conn. (net.Conn)`