package agent

import (
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func safeTool(name string) tool.Tool {
	return tool.Tool{
		Name:            name,
		ConcurrencySafe: true,
	}
}

func unsafeTool(name string) tool.Tool {
	return tool.Tool{
		Name:            name,
		ConcurrencySafe: false,
	}
}

func makePrepared(tools ...tool.Tool) []preparedToolCall {
	var calls []preparedToolCall

	for i, t := range tools {
		calls = append(calls, preparedToolCall{
			index: i,
			call:  ToolCall{ID: t.Name, Name: t.Name},
			tool:  &t,
		})
	}

	return calls
}

func TestPartitionToolCalls_AllSafe(t *testing.T) {
	calls := makePrepared(safeTool("read1"), safeTool("read2"), safeTool("read3"))
	batches := partitionToolCalls(calls)

	if len(batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batches))
	}

	if !batches[0].concurrent {
		t.Error("expected batch to be concurrent")
	}

	if len(batches[0].calls) != 3 {
		t.Errorf("expected 3 calls in batch, got %d", len(batches[0].calls))
	}
}

func TestPartitionToolCalls_AllUnsafe(t *testing.T) {
	calls := makePrepared(unsafeTool("write1"), unsafeTool("write2"), unsafeTool("edit1"))
	batches := partitionToolCalls(calls)

	if len(batches) != 3 {
		t.Fatalf("expected 3 batches (one per unsafe tool), got %d", len(batches))
	}

	for i, b := range batches {
		if b.concurrent {
			t.Errorf("batch %d should not be concurrent", i)
		}
		if len(b.calls) != 1 {
			t.Errorf("batch %d should have 1 call, got %d", i, len(b.calls))
		}
	}
}

func TestPartitionToolCalls_SafeUnsafeSafe(t *testing.T) {
	calls := makePrepared(
		safeTool("read1"), safeTool("read2"),
		unsafeTool("write1"),
		safeTool("read3"), safeTool("read4"),
	)
	batches := partitionToolCalls(calls)

	if len(batches) != 3 {
		t.Fatalf("expected 3 batches [safe, unsafe, safe], got %d", len(batches))
	}

	if !batches[0].concurrent || len(batches[0].calls) != 2 {
		t.Errorf("batch 0: expected concurrent with 2 calls, got concurrent=%v len=%d",
			batches[0].concurrent, len(batches[0].calls))
	}

	if batches[1].concurrent || len(batches[1].calls) != 1 {
		t.Errorf("batch 1: expected non-concurrent with 1 call, got concurrent=%v len=%d",
			batches[1].concurrent, len(batches[1].calls))
	}

	if !batches[2].concurrent || len(batches[2].calls) != 2 {
		t.Errorf("batch 2: expected concurrent with 2 calls, got concurrent=%v len=%d",
			batches[2].concurrent, len(batches[2].calls))
	}
}

func TestPartitionToolCalls_UnsafeSafeUnsafe(t *testing.T) {
	calls := makePrepared(
		unsafeTool("write1"),
		safeTool("read1"), safeTool("read2"),
		unsafeTool("write2"),
	)
	batches := partitionToolCalls(calls)

	if len(batches) != 3 {
		t.Fatalf("expected 3 batches [unsafe, safe, unsafe], got %d", len(batches))
	}

	if batches[0].concurrent {
		t.Error("batch 0 should not be concurrent")
	}

	if !batches[1].concurrent || len(batches[1].calls) != 2 {
		t.Errorf("batch 1: expected concurrent with 2 calls")
	}

	if batches[2].concurrent {
		t.Error("batch 2 should not be concurrent")
	}
}

func TestPartitionToolCalls_SingleSafe(t *testing.T) {
	calls := makePrepared(safeTool("read1"))
	batches := partitionToolCalls(calls)

	if len(batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batches))
	}

	if !batches[0].concurrent {
		t.Error("single safe tool should still be marked concurrent")
	}
}

func TestPartitionToolCalls_SingleUnsafe(t *testing.T) {
	calls := makePrepared(unsafeTool("write1"))
	batches := partitionToolCalls(calls)

	if len(batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batches))
	}

	if batches[0].concurrent {
		t.Error("single unsafe tool should not be concurrent")
	}
}

func TestPartitionToolCalls_Empty(t *testing.T) {
	batches := partitionToolCalls(nil)

	if len(batches) != 0 {
		t.Fatalf("expected 0 batches for nil input, got %d", len(batches))
	}
}

func TestPartitionToolCalls_PreservesIndexOrder(t *testing.T) {
	calls := makePrepared(
		safeTool("a"), safeTool("b"),
		unsafeTool("c"),
		safeTool("d"),
	)
	batches := partitionToolCalls(calls)

	// Verify that index assignment is correct
	idx := 0
	for _, batch := range batches {
		for _, call := range batch.calls {
			if call.index != idx {
				t.Errorf("expected index %d, got %d (tool %s)", idx, call.index, call.call.Name)
			}
			idx++
		}
	}
}

func TestPartitionToolCalls_NilToolTreatedAsUnsafe(t *testing.T) {
	calls := []preparedToolCall{
		{call: ToolCall{Name: "unknown"}, tool: nil},
		{call: ToolCall{Name: "read"}, tool: func() *tool.Tool { t := safeTool("read"); return &t }()},
	}
	batches := partitionToolCalls(calls)

	if len(batches) != 2 {
		t.Fatalf("expected 2 batches (nil-tool is unsafe), got %d", len(batches))
	}

	if batches[0].concurrent {
		t.Error("nil-tool batch should not be concurrent")
	}

	if !batches[1].concurrent {
		t.Error("safe-tool batch should be concurrent")
	}
}
