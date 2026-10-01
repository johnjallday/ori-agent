package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// cloneInMemoryWorkspace detaches persisted data without a JSON round trip.
// Test fixtures can carry typed slices, maps, and exact numbers in interface
// fields; JSON decoding would erase their Go types. Runtime locks and indexes
// are not copied, and mutation intent ends at the persistence boundary.
func cloneInMemoryWorkspace(ws *Workspace) (*Workspace, error) {
	if ws == nil {
		return nil, errors.New("nil workspace")
	}
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	if _, err := json.Marshal(ws); err != nil {
		return nil, fmt.Errorf("cannot snapshot workspace: %w", err)
	}
	value, err := cloneMemorySnapshotValue(reflect.ValueOf(ws), make(map[memorySnapshotVisit]bool))
	if err != nil {
		return nil, fmt.Errorf("cannot snapshot workspace: %w", err)
	}
	clone := value.Interface().(*Workspace)
	clone.missionLoaded = ws.missionLoaded
	clone.rebuildTaskIndex()
	return clone, nil
}

type memorySnapshotVisit struct {
	typ     reflect.Type
	pointer uintptr
	length  int
}

func cloneMemorySnapshotValue(value reflect.Value, ancestors map[memorySnapshotVisit]bool) (reflect.Value, error) {
	// Time's unexported location is immutable and its value copy is safe,
	// including named types with the same underlying representation.
	if value.Kind() == reflect.Struct && value.Type().ConvertibleTo(reflect.TypeFor[time.Time]()) {
		return value, nil
	}
	// JSON validation rejects ordinary cycles. Track the copy's ancestry too:
	// custom JSON marshalers can hide cycles in their public Go fields.
	if value.Kind() == reflect.Pointer || value.Kind() == reflect.Map || value.Kind() == reflect.Slice {
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		visit := memorySnapshotVisit{typ: value.Type(), pointer: value.Pointer()}
		if value.Kind() == reflect.Slice {
			visit.length = value.Len()
		}
		if ancestors[visit] {
			return reflect.Value{}, errors.New("cyclic snapshot data")
		}
		ancestors[visit] = true
		defer delete(ancestors, visit)
	}

	switch value.Kind() {
	case reflect.Interface:
		clone := reflect.New(value.Type()).Elem()
		if !value.IsNil() {
			item, err := cloneMemorySnapshotValue(value.Elem(), ancestors)
			if err != nil {
				return reflect.Value{}, err
			}
			clone.Set(item)
		}
		return clone, nil
	case reflect.Pointer:
		item, err := cloneMemorySnapshotValue(value.Elem(), ancestors)
		if err != nil {
			return reflect.Value{}, err
		}
		clone := reflect.New(value.Type().Elem())
		clone.Elem().Set(item)
		return clone.Convert(value.Type()), nil
	case reflect.Struct:
		clone := reflect.New(value.Type()).Elem()
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if field.IsExported() && field.Tag.Get("json") != "-" {
				item, err := cloneMemorySnapshotValue(value.Field(i), ancestors)
				if err != nil {
					return reflect.Value{}, err
				}
				clone.Field(i).Set(item)
			}
		}
		return clone, nil
	case reflect.Map:
		clone := reflect.MakeMapWithSize(value.Type(), value.Len())
		entries := value.MapRange()
		for entries.Next() {
			key, err := cloneMemorySnapshotValue(entries.Key(), ancestors)
			if err != nil {
				return reflect.Value{}, err
			}
			item, err := cloneMemorySnapshotValue(entries.Value(), ancestors)
			if err != nil {
				return reflect.Value{}, err
			}
			clone.SetMapIndex(key, item)
		}
		return clone, nil
	case reflect.Slice, reflect.Array:
		var clone reflect.Value
		if value.Kind() == reflect.Slice {
			clone = reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		} else {
			clone = reflect.New(value.Type()).Elem()
		}
		for i := range value.Len() {
			item, err := cloneMemorySnapshotValue(value.Index(i), ancestors)
			if err != nil {
				return reflect.Value{}, err
			}
			clone.Index(i).Set(item)
		}
		return clone, nil
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return reflect.Value{}, fmt.Errorf("unsupported snapshot value: %s", value.Type())
	default:
		return value, nil
	}
}
