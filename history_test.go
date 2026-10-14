package main

import (
	"reflect"
	"testing"
)

func TestRemoveByIndexes(t *testing.T) {
	src := []int{0, 1, 2, 3, 4, 5}

	cases := []struct {
		name string
		idxs []int
		want []int
	}{
		{"单删", []int{2}, []int{0, 1, 3, 4, 5}},
		{"多删乱序", []int{4, 1, 2}, []int{0, 3, 5}},
		{"全删", []int{0, 1, 2, 3, 4, 5}, []int{}},
		{"重复索引去重", []int{1, 1, 3}, []int{0, 2, 4, 5}},
		{"越界忽略", []int{1, 99, -2}, []int{0, 2, 3, 4, 5}},
		{"空索引", []int{}, []int{0, 1, 2, 3, 4, 5}},
		{"尾部", []int{5}, []int{0, 1, 2, 3, 4}},
		{"头部", []int{0}, []int{1, 2, 3, 4, 5}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := removeByIndexes(src, c.idxs)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("removeByIndexes(%v, %v) = %v, want %v", src, c.idxs, got, c.want)
			}
			// 确认不修改入参
			if len(src) != 6 {
				t.Fatalf("src mutated: %v", src)
			}
		})
	}
}
