// Copyright (c) 2014 Derek Parker
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// This file may have been modified by CloudWeGo authors. All CloudWeGo
// Modifications are Copyright 2024 CloudWeGo Authors.

package proc

import (
	"debug/dwarf"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/reader"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
)

const (
	goDictionaryName = ".dict"
	goClosurePtr     = ".closureptr"
)

const fakeAddressUnresolv = 0xbeed000000000000

type myEvalScope struct {
	proc.EvalScope

	dictAddr uint64 // dictionary address for instantiated generic functions

	// enclosingRangeScopes []*proc.EvalScope
	// rangeFrames          []proc.Stackframe
}

func (scope *myEvalScope) Locals(mds []proc.ModuleData) ([]*ReferenceVariable, error) {
	// var scopes [][]*Variable
	vars0, err := scope.simpleLocals(mds)
	if err != nil {
		return nil, err
	}
	return vars0, nil
	// TODO: support range-over-func
	/*
		if scope.Fn.extra(scope.BinInfo).rangeParent == nil || scope.target == nil || scope.g == nil {
			return vars0, nil
		}

		scopes = append(scopes, vars0)

		if scope.rangeFrames == nil {
			scope.rangeFrames, err = rangeFuncStackTrace(scope.target, scope.g)
			if err != nil {
				return nil, err
			}
			scope.rangeFrames = scope.rangeFrames[1:]
			scope.enclosingRangeScopes = make([]*EvalScope, len(scope.rangeFrames))
		}
		for i, scope2 := range scope.enclosingRangeScopes {
			if i == len(scope.enclosingRangeScopes)-1 {
				// Last one is the caller frame, we shouldn't check it
				break
			}
			if scope2 == nil {
				scope2 = FrameToScope(scope.target, scope.target.Memory(), scope.g, scope.threadID, scope.rangeFrames[i:]...)
				scope.enclosingRangeScopes[i] = scope2
			}
			vars, err := scope2.simpleLocals0(flags, mds)
			if err != nil {
				return nil, err
			}
			scopes = append(scopes, vars)
		}

		vars := []*Variable{}
		for i := len(scopes) - 1; i >= 0; i-- {
			vars = append(vars, scopes[i]...)
		}

		// Apply shadowning
		lvn := map[string]*Variable{}
		for _, v := range vars {
			if otherv := lvn[v.Name]; otherv != nil {
				otherv.Flags |= VariableShadowed
			}
			lvn[v.Name] = v
		}
		return vars, nil
	*/
}

func (scope *myEvalScope) simpleLocals(mds []proc.ModuleData) ([]*ReferenceVariable, error) {
	if scope.Fn == nil {
		return nil, errors.New("unable to find function context")
	}

	if image(&scope.EvalScope).Stripped() {
		return nil, errors.New("unable to find locals: no debug information present in binary")
	}

	dwarfTree, err := getDwarfTree(image(&scope.EvalScope), getFunctionOffset(scope.Fn))
	if err != nil {
		return nil, err
	}

	variablesFlags := reader.VariablesOnlyVisible
	if scope.BinInfo.Producer() != "" && goversion.ProducerAfterOrEqual(scope.BinInfo.Producer(), 1, 15) {
		variablesFlags |= reader.VariablesTrustDeclLine
	}

	varEntries := reader.Variables(dwarfTree, scope.PC, scope.Line, variablesFlags)

	// look for dictionary entry
	if scope.dictAddr == 0 {
		for _, entry := range varEntries {
			name, _ := entry.Val(dwarf.AttrName).(string)
			if name == goDictionaryName {
				dictVar, err := extractVarInfoFromEntry(scope.BinInfo, image(&scope.EvalScope), scope.Regs, scope.Mem, entry.Tree, 0, mds)
				if err != nil {
					logflags.DebuggerLogger().Errorf("could not load %s variable: %v", name, err)
				} else {
					scope.dictAddr, err = readUintRaw(dictVar.mem, uint64(dictVar.Addr), int64(scope.BinInfo.Arch.PtrSize()))
					if err != nil {
						logflags.DebuggerLogger().Errorf("could not load %s variable: %v", name, err)
					}
				}
				break
			}
		}
	}

	vars := make([]*ReferenceVariable, 0, len(varEntries))
	depths := make([]int, 0, len(varEntries))
	for _, entry := range varEntries {
		name, _ := entry.Val(dwarf.AttrName).(string)
		if name == goDictionaryName || name == goClosurePtr || strings.HasPrefix(name, "#state") || strings.HasPrefix(name, "&#state") || strings.HasPrefix(name, "#next") || strings.HasPrefix(name, "&#next") || strings.HasPrefix(name, "#yield") {
			continue
		}
		if rangeParentName(scope.Fn) != "" {
			// Skip return values and closure variables for range-over-func closure bodies
			if strings.HasPrefix(name, "~") {
				continue
			}
			if entry.Val(godwarf.AttrGoClosureOffset) != nil {
				continue
			}
		}
		val, err := extractVarInfoFromEntry(scope.BinInfo, image(&scope.EvalScope), scope.Regs, scope.Mem, entry.Tree, scope.dictAddr, mds)
		if err != nil {
			// skip variables that we can't parse yet
			continue
		}
		vars = append(vars, val)
		depth := entry.Depth
		if entry.Tag == dwarf.TagFormalParameter {
			if depth <= 1 {
				depth = 0
			}
		}
		depths = append(depths, depth)
	}
	if len(vars) == 0 {
		return vars, nil
	}
	sort.Stable(&variablesByDepthAndDeclLine{vars, depths})
	return vars, nil
}

// Extracts the name and type of a variable from a dwarf entry
// then executes the instructions given in the  DW_AT_location attribute to grab the variable's address
func extractVarInfoFromEntry(bi *proc.BinaryInfo, image *proc.Image, regs op.DwarfRegisters, mem proc.MemoryReadWriter, entry *godwarf.Tree, dictAddr uint64, mds []proc.ModuleData) (*ReferenceVariable, error) {
	if entry.Tag != dwarf.TagFormalParameter && entry.Tag != dwarf.TagVariable {
		return nil, fmt.Errorf("invalid entry tag, only supports FormalParameter and Variable, got %s", entry.Tag.String())
	}

	n, t, err := readVarEntry(entry, image)
	if err != nil {
		return nil, err
	}

	t, err = resolveParametricType(bi, mem, t, dictAddr, mds)
	if err != nil {
		// Log the error, keep going with t, which will be the shape type
		logflags.DebuggerLogger().Errorf("could not resolve parametric type of %s: %v", n, err)
	}

	addr, pieces, _, _ := bi.Location(entry, dwarf.AttrLocation, regs.PC(), regs, mem)
	uaddr := uint64(addr)
	if pieces != nil {
		cmem, _ := proc.CreateCompositeMemory(mem, bi.Arch, regs, pieces, t.Common().ByteSize)
		if cmem != nil {
			uaddr = fakeAddressUnresolv
			mem = cmem
		}
	}

	v := newReferenceVariable(Address(uaddr), n, resolveTypedef(t), mem, nil)
	return v, nil
}

// resolveParametricType returns the real type of t if t is a parametric
// type, by reading the correct dictionary entry.
func resolveParametricType(bi *proc.BinaryInfo, mem proc.MemoryReadWriter, t godwarf.Type, dictAddr uint64, mds []proc.ModuleData) (godwarf.Type, error) {
	ptyp, _ := t.(*godwarf.ParametricType)
	if ptyp == nil {
		return t, nil
	}
	if dictAddr == 0 {
		return ptyp.TypedefType.Type, errors.New("parametric type without a dictionary")
	}
	rtypeAddr, err := readUintRaw(mem, dictAddr+uint64(ptyp.DictIndex*int64(bi.Arch.PtrSize())), int64(bi.Arch.PtrSize()))
	if err != nil {
		return ptyp.TypedefType.Type, err
	}
	runtimeType, err := findType(bi, runtimeTypeTypename(bi))
	if err != nil {
		return ptyp.TypedefType.Type, err
	}
	_type := newVariable("", rtypeAddr, runtimeType, bi, mem)

	typ, _, err := proc.RuntimeTypeToDIE(_type, 0, mds)
	if err != nil {
		return ptyp.TypedefType.Type, err
	}

	return typ, nil
}

func runtimeTypeTypename(bi *proc.BinaryInfo) string {
	if goversion.ProducerAfterOrEqual(bi.Producer(), 1, 21) {
		return "internal/abi.Type"
	}
	return "runtime._type"
}

type variablesByDepthAndDeclLine struct {
	vars   []*ReferenceVariable
	depths []int
}

func (v *variablesByDepthAndDeclLine) Len() int { return len(v.vars) }

func (v *variablesByDepthAndDeclLine) Less(i, j int) bool {
	return v.depths[i] < v.depths[j]
}

func (v *variablesByDepthAndDeclLine) Swap(i, j int) {
	v.depths[i], v.depths[j] = v.depths[j], v.depths[i]
	v.vars[i], v.vars[j] = v.vars[j], v.vars[i]
}
