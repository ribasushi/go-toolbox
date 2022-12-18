package cmn

import (
	"fmt"
	"reflect"
	"sort"
)

func SortedMapKeys(m interface{}) []string { //nolint:revive
	v := reflect.ValueOf(m)
	if v.Kind() != reflect.Map {
		panic(fmt.Sprintf("input type not a map: %v", v))
	}
	avail := make([]string, 0, v.Len())
	for _, k := range v.MapKeys() {
		avail = append(avail, k.String())
	}
	sort.Strings(avail)
	return avail
}
