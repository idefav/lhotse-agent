package cow

import (
	"sync"
	"sync/atomic"
)

type Array struct {
	v atomic.Value
	m sync.Mutex // used only by writers
}

func (array *Array) copy(dst []interface{}, src []interface{}) {
	for k, v := range src {
		dst[k] = v // copy all data from the current object to the new one
	}
}

func (array *Array) dup(src []interface{}) (dst []interface{}) {
	dst = make([]interface{}, len(src))
	array.copy(dst, src)
	return dst
}

func NewArray(m []interface{}) *Array {
	cowm := Array{}
	cowm.v.Store(cowm.dup(m))
	return &cowm
}

func (array *Array) Get(i int) (val interface{}) {
	// No lock needed!
	m := array.v.Load().([]interface{})
	return m[i]
}

func (array *Array) Set(i int, val interface{}) {
	array.m.Lock()
	defer array.m.Unlock()

	src := array.v.Load().([]interface{})
	dst := array.dup(src)
	dst[i] = val
	array.v.Store(dst)
}

func (array *Array) Update(m []interface{}) {
	array.m.Lock()
	defer array.m.Unlock()

	src := array.v.Load().([]interface{})
	dst := array.dup(src)
	array.copy(dst, m)
	array.v.Store(dst)
}

func (array *Array) Remove(i int) {
	array.m.Lock()
	defer array.m.Unlock()

	src := array.v.Load().([]interface{})
	dst := array.dup(src)
	dst = append(dst[:i], dst[i+1:]...)
	array.v.Store(dst)
}

func (array *Array) Reset(m []interface{}) {
	array.m.Lock()
	defer array.m.Unlock()

	array.v.Store(array.dup(m))
}
