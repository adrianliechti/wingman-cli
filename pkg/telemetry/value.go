package telemetry

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

// StructuredValue converts a JSON-compatible Go value into OpenTelemetry's
// structured attribute representation. Unsupported and non-finite values are
// omitted rather than serialized as misleading strings.
func StructuredValue(value any) (attribute.Value, bool) {
	switch value := value.(type) {
	case nil:
		return attribute.Value{}, false
	case bool:
		return attribute.BoolValue(value), true
	case string:
		return attribute.StringValue(value), true
	case json.Number:
		if integer, err := value.Int64(); err == nil {
			return attribute.Int64Value(integer), true
		}
		floating, err := value.Float64()
		return attribute.Float64Value(floating), err == nil && !math.IsNaN(floating) && !math.IsInf(floating, 0)
	case int:
		return attribute.IntValue(value), true
	case int64:
		return attribute.Int64Value(value), true
	case float64:
		return attribute.Float64Value(value), !math.IsNaN(value) && !math.IsInf(value, 0)
	case []any:
		values := make([]attribute.Value, 0, len(value))
		for _, item := range value {
			if converted, ok := StructuredValue(item); ok {
				values = append(values, converted)
			}
		}
		return attribute.SliceValue(values...), true
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		attrs := make([]attribute.KeyValue, 0, len(keys))
		for _, key := range keys {
			if converted, ok := StructuredValue(value[key]); ok {
				attrs = append(attrs, attribute.KeyValue{Key: attribute.Key(key), Value: converted})
			}
		}
		return attribute.MapValue(attrs...), true
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return attribute.Value{}, false
		}
		decoder := json.NewDecoder(strings.NewReader(string(encoded)))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return attribute.Value{}, false
		}
		return StructuredValue(decoded)
	}
}

// StructuredObjectValue converts an object-like JSON value for semantic
// convention fields whose schema requires an object.
func StructuredObjectValue(value any) (attribute.Value, bool) {
	converted, ok := StructuredValue(value)
	return converted, ok && converted.Type() == attribute.MAP
}
