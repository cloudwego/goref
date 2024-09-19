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
	"github.com/go-delve/delve/pkg/proc"
)

const (
	cacheEnabled   = true
	cacheThreshold = 1024 * 1024 * 1024 // 1GB
)

type memCache struct {
	loaded    bool
	cacheAddr uint64
	cache     []byte
	mem       proc.MemoryReadWriter
}

func (m *memCache) contains(addr uint64, size int) bool {
	end := addr + uint64(size)
	if end < addr {
		// overflow
		return false
	}
	return addr >= m.cacheAddr && end <= m.cacheAddr+uint64(len(m.cache))
}

func (m *memCache) ReadMemory(data []byte, addr uint64) (n int, err error) {
	if m.contains(addr, len(data)) {
		if !m.loaded {
			_, err := m.mem.ReadMemory(m.cache, m.cacheAddr)
			if err != nil {
				return 0, err
			}
			m.loaded = true
		}
		copy(data, m.cache[addr-m.cacheAddr:])
		return len(data), nil
	}

	return m.mem.ReadMemory(data, addr)
}

func (m *memCache) WriteMemory(addr uint64, data []byte) (written int, err error) {
	return m.mem.WriteMemory(addr, data)
}

func cacheMemory(mem proc.MemoryReadWriter, addr uint64, size int) proc.MemoryReadWriter {
	if !cacheEnabled {
		return mem
	}
	if size <= 0 {
		return mem
	}
	if addr+uint64(size) < addr {
		// overflow
		return mem
	}
	if size > cacheThreshold {
		return mem
	}
	switch cacheMem := mem.(type) {
	case *memCache:
		if cacheMem.contains(addr, size) {
			return mem
		} else {
			return &memCache{false, addr, make([]byte, size), cacheMem.mem}
		}
	}
	return &memCache{false, addr, make([]byte, size), mem}
}
