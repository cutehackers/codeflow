package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/detect"
	"codeflow/internal/harvest"
)

// ---------------------------------------------------------------------------
// TIER 2: Boundary & Corner Cases (60+ test cases across 12 boundary categories)
// ---------------------------------------------------------------------------

// Boundary 1: Empty functions, stubs, and void returns (5 tests)
func TestTier2_Boundary1_EmptyFunctionsAndStubs(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 1.1: Empty arrow handler in functional component
	t.Run("empty_arrow_handler", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const StubForm = () => {
  const handleStub = () => {};
  return <button onClick={handleStub} />;
};`
		os.WriteFile(filepath.Join(tempDir, "StubForm.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "StubForm.tsx", "StubForm.handleStub", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		if len(payload.Steps) == 0 {
			t.Errorf("expected minItems 1 fallback step for empty stub handler")
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 1.2: Empty function declaration
	t.Run("empty_function_declaration", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export function emptyHandler() {}`
		os.WriteFile(filepath.Join(tempDir, "empty.ts"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "empty.ts", "emptyHandler", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 1.3: Function with only whitespace and comments
	t.Run("function_with_only_comments", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const CommentOnly = () => {
  const handleAction = () => {
    // TODO: implement later
    /* multi-line comment stub */
  };
  return <div onClick={handleAction} />;
};`
		os.WriteFile(filepath.Join(tempDir, "CommentOnly.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "CommentOnly.tsx", "CommentOnly.handleAction", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 1.4: Function returning empty object literal
	t.Run("function_returning_empty_object", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const ObjectReturn = () => {
  const getEmptyConfig = () => {
    return {};
  };
  return <button onClick={getEmptyConfig} />;
};`
		os.WriteFile(filepath.Join(tempDir, "ObjectReturn.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "ObjectReturn.tsx", "ObjectReturn.getEmptyConfig", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 1.5: Empty class method stub
	t.Run("empty_class_method", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export class StubService {
  execute() {}
}`
		os.WriteFile(filepath.Join(tempDir, "StubService.ts"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "StubService.ts", "StubService.execute", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})
}

// Boundary 2: Deeply nested closures and callbacks (4+ levels deep) (5 tests)
func TestTier2_Boundary2_DeeplyNestedClosures(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 2.1: 4-level nested closure
	t.Run("four_level_nested_closure", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const DeepComponent = () => {
  const handleLevel1 = () => {
    const fn2 = () => {
      const fn3 = () => {
        const fn4 = () => {
          logger.log("deep execution");
        };
        fn4();
      };
      fn3();
    };
    fn2();
  };
  return <button onClick={handleLevel1} />;
};`
		os.WriteFile(filepath.Join(tempDir, "DeepComponent.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "DeepComponent.tsx", "DeepComponent.handleLevel1", 4)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 2.2: Nested try/catch/finally blocks with calls
	t.Run("nested_try_catch_finally", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const TryCatchView = () => {
  const handleSubmit = async () => {
    try {
      await api.v1.auth.login();
    } catch (e) {
      logger.error(e);
    } finally {
      cleanup();
    }
  };
  return <button onClick={handleSubmit} />;
};`
		os.WriteFile(filepath.Join(tempDir, "TryCatchView.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "TryCatchView.tsx", "TryCatchView.handleSubmit", 2)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 2.3: Nested switch inside if condition
	t.Run("nested_switch_inside_if", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const SwitchView = () => {
  const handleAction = (type: string) => {
    if (type) {
      switch (type) {
        case 'A':
          serviceA.run();
          break;
        case 'B':
          serviceB.run();
          break;
        default:
          fallback.run();
      }
    }
  };
  return <div onClick={() => handleAction('A')} />;
};`
		os.WriteFile(filepath.Join(tempDir, "SwitchView.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "SwitchView.tsx", "SwitchView.handleAction", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 2.4: Higher-order function returning async handler
	t.Run("higher_order_handler_generator", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const HofView = () => {
  const createSubmitHandler = (endpoint: string) => {
    return async (e) => {
      e.preventDefault();
      await fetch(endpoint);
    };
  };
  return <form onSubmit={createSubmitHandler('/api/data')} />;
};`
		os.WriteFile(filepath.Join(tempDir, "HofView.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "HofView.tsx", "HofView.createSubmitHandler", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 2.5: Deep callback inside Array.forEach
	t.Run("nested_callback_in_array_loop", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const LoopView = () => {
  const handleProcessAll = (items: string[]) => {
    items.forEach((item) => {
      if (item) {
        processor.process(item);
      }
    });
  };
  return <button onClick={() => handleProcessAll(['a', 'b'])} />;
};`
		os.WriteFile(filepath.Join(tempDir, "LoopView.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "LoopView.tsx", "LoopView.handleProcessAll", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})
}

// Boundary 3: Multi-dot chains with 4+ dots (5 tests)
func TestTier2_Boundary3_LongMemberChains(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	chains := []struct {
		name string
		code string
	}{
		{"five_dots", `await client.v1.orgs.teams.users.list();`},
		{"six_dots", `await a.b.c.d.e.f.execute();`},
		{"array_index_chain", `await api.v1.endpoints.routes.get();`},
		{"fluent_builder_chain", `await builder.withAuth().withHeader().withPayload().send();`},
		{"nested_config_chain", `await config.services.gateway.v2.proxy.forward();`},
	}

	for _, tc := range chains {
		t.Run("chain_"+tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			src := `export const ChainComp = () => {
  const onTrigger = async () => {
    ` + tc.code + `
  };
  return <button onClick={onTrigger} />;
};`
			os.WriteFile(filepath.Join(tempDir, "ChainComp.tsx"), []byte(src), 0o644)
			payload, err := sliceHelper(t, pool, ctx, tempDir, "ChainComp.tsx", "ChainComp.onTrigger", 1)
			if err != nil {
				t.Fatalf("slice failed for %s: %v", tc.name, err)
			}
			validateContract(t, "sliced-payload.schema.json", payload)
		})
	}
}

// Boundary 4: Anonymous inline JSX callbacks & arrow expressions (5 tests)
func TestTier2_Boundary4_AnonymousInlineJsxCallbacks(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 4.1: Simple inline setter onClick={() => setOpen(true)}
	t.Run("inline_state_setter", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const ModalView = () => {
  const [open, setOpen] = useState(false);
  return <button onClick={() => setOpen(true)}>Open</button>;
};`
		os.WriteFile(filepath.Join(tempDir, "ModalView.tsx"), []byte(src), 0o644)
		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		// Slicing ModalView should succeed without throwing
		payload, err := sliceHelper(t, pool, ctx, tempDir, "ModalView.tsx", "ModalView", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 4.2: Inline event handler with preventDefault
	t.Run("inline_form_prevent_default", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const QuickForm = () => {
  return <form onSubmit={(e) => { e.preventDefault(); console.log('submit'); }} />;
};`
		os.WriteFile(filepath.Join(tempDir, "QuickForm.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "QuickForm.tsx", "QuickForm", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 4.3: Multiple inline handlers in single component
	t.Run("multiple_inline_handlers", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const MultiInline = () => {
  return (
    <div>
      <button onClick={() => console.log(1)}>1</button>
      <button onClick={() => console.log(2)}>2</button>
      <button onClick={() => console.log(3)}>3</button>
    </div>
  );
};`
		os.WriteFile(filepath.Join(tempDir, "MultiInline.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "MultiInline.tsx", "MultiInline", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 4.4: Conditional inline callback handler
	t.Run("conditional_inline_callback", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const ConditionalBtn = ({ isAllowed }) => {
  return <button onClick={isAllowed ? () => api.delete() : undefined}>Delete</button>;
};`
		os.WriteFile(filepath.Join(tempDir, "ConditionalBtn.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "ConditionalBtn.tsx", "ConditionalBtn", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 4.5: Inline callback passing argument to external function
	t.Run("inline_callback_passing_arg", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const ListRow = ({ id, onSelect }) => {
  return <div onClick={() => onSelect(id)}>Row {id}</div>;
};`
		os.WriteFile(filepath.Join(tempDir, "ListRow.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "ListRow.tsx", "ListRow", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})
}

// Boundary 5: Complex TypeScript generics, union/intersection types (5 tests)
func TestTier2_Boundary5_GenericsAndUnionTypes(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 5.1: Generic component with extends clause
	t.Run("generic_component_with_extends", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const GenericForm = <T extends Record<string, any>>(props: { data: T }) => {
  const handleSubmit = (item: T) => {
    console.log(item);
  };
  return <form onSubmit={() => handleSubmit(props.data)} />;
};`
		os.WriteFile(filepath.Join(tempDir, "GenericForm.tsx"), []byte(src), 0o644)
		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) == 0 {
			t.Errorf("expected candidates harvested from GenericForm")
		}
	})

	// 5.2: Multi-parameter generics in function
	t.Run("multi_parameter_generics", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export async function handleDataTransform<T, R extends Result<T>>(input: T): Promise<R> {
  return transform(input);
}`
		os.WriteFile(filepath.Join(tempDir, "transform.ts"), []byte(src), 0o644)
		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) == 0 {
			t.Errorf("expected handleDataTransform candidate")
		}
	})

	// 5.3: Union and intersection types in props
	t.Run("union_and_intersection_props", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const UnionView = (props: (AdminProps & BaseProps) | GuestProps) => {
  const onAction = () => {};
  return <button onClick={onAction} />;
};`
		os.WriteFile(filepath.Join(tempDir, "UnionView.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "UnionView.tsx", "UnionView.onAction", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 5.4: Type assertions with 'as const'
	t.Run("type_assertions_in_handler", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const AssertComp = () => {
  const onSave = () => {
    const payload = { role: 'admin' as const, id: 101 as number };
    api.save(payload);
  };
  return <button onClick={onSave} />;
};`
		os.WriteFile(filepath.Join(tempDir, "AssertComp.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "AssertComp.tsx", "AssertComp.onSave", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 5.5: Generic Hook definition
	t.Run("generic_hook_definition", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export function useGenericMutation<TData, TVariables = void>() {
  const mutate = async (variables: TVariables): Promise<TData> => {
    return fetcher(variables);
  };
  return { mutate };
}`
		os.WriteFile(filepath.Join(tempDir, "useGenericMutation.ts"), []byte(src), 0o644)
		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) == 0 {
			t.Errorf("expected useGenericMutation candidate")
		}
	})
}

// Boundary 6: Multi-byte UTF-8 Unicode characters and byte offset invariants (5 tests)
func TestTier2_Boundary6_MultiByteUnicodeHandling(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	unicodeSnippets := []struct {
		name string
		code string
	}{
		{"korean_comments", `// 사용자 인증 처리 함수
export const KoreanAuth = () => {
  const handleSubmit = async () => {
    // 서버로 결제 요청 전송 🚀
    await api.orders.checkout("주문내용");
  };
  return <button onClick={handleSubmit}>결제하기</button>;
};`},
		{"japanese_text", `// ユーザー登録処理
export const JapaneseView = () => {
  const onRegister = () => {
    console.log("ユーザー登録");
  };
  return <button onClick={onRegister}>登録</button>;
};`},
		{"emoji_strings", `export const EmojiComp = () => {
  const handleEmoji = () => {
    const str = "🎉🔥✨🚀👍";
    logger.log(str);
  };
  return <div onClick={handleEmoji} />;
};`},
		{"mixed_special_characters", `export const SpecialChars = () => {
  const onSpecial = () => {
    const query = "param=value&name=홍길동&status=활성";
    api.search(query);
  };
  return <button onClick={onSpecial} />;
};`},
		{"cyrillic_symbols", `// Обработка данных
export const CyrillicView = () => {
  const onProcess = () => {
    api.send("Привет мир");
  };
  return <button onClick={onProcess}>Отправить</button>;
};`},
	}

	for _, tc := range unicodeSnippets {
		t.Run("unicode_"+tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			fileName := tc.name + ".tsx"
			filePath := filepath.Join(tempDir, fileName)
			os.WriteFile(filePath, []byte(tc.code), 0o644)

			var resp struct {
				Candidates []harvest.Candidate `json:"candidates"`
			}
			err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
			if err != nil {
				t.Fatalf("harvest failed: %v", err)
			}
			if len(resp.Candidates) == 0 {
				t.Fatalf("expected candidates in %s", tc.name)
			}

			// Slice first candidate
			first := resp.Candidates[0]
			parts := strings.Split(first.EntrySymbolPath, "#")
			payload, err := sliceHelper(t, pool, ctx, tempDir, parts[0], parts[1], 1)
			if err != nil {
				t.Fatalf("slice failed: %v", err)
			}

			fileBytes, _ := os.ReadFile(filePath)
			for _, step := range payload.Steps {
				a := step.Anchor
				if a.ByteRange[0] < 0 || a.ByteRange[1] > len(fileBytes) || a.ByteRange[0] > a.ByteRange[1] {
					t.Errorf("invalid byteRange [%d, %d] for file length %d", a.ByteRange[0], a.ByteRange[1], len(fileBytes))
				}
				slice := fileBytes[a.ByteRange[0]:a.ByteRange[1]]
				if sha256Hex(slice) != a.SpanHash {
					t.Errorf("spanHash mismatch on UTF-8 code: got %s, want %s", a.SpanHash, sha256Hex(slice))
				}
			}
		})
	}
}

// Boundary 7: Corrupted or missing package.json in topology detection (5 tests)
func TestTier2_Boundary7_CorruptedOrMissingPackageJson(t *testing.T) {
	// 7.1: Missing package.json in directory with components
	t.Run("missing_package_json_with_components", func(t *testing.T) {
		tempDir := t.TempDir()
		os.MkdirAll(filepath.Join(tempDir, "src", "components"), 0o755)
		pattern, err := detect.DetectArchitecturePattern(tempDir)
		if err != nil {
			t.Fatalf("detection failed: %v", err)
		}
		if pattern == "" {
			t.Errorf("expected valid fallback pattern")
		}
	})

	// 7.2: Corrupted JSON in package.json
	t.Run("corrupted_json_in_package_json", func(t *testing.T) {
		tempDir := t.TempDir()
		os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(`{ invalid json: true,`), 0o644)
		os.MkdirAll(filepath.Join(tempDir, "app"), 0o755)
		os.WriteFile(filepath.Join(tempDir, "app", "page.tsx"), []byte("export default () => null;"), 0o644)

		pattern, err := detect.DetectArchitecturePattern(tempDir)
		if err != nil {
			t.Fatalf("detection should not crash on malformed package.json: %v", err)
		}
		if pattern != detect.PatternNextAppRouter {
			t.Errorf("expected NextAppRouter from app/page.tsx, got %q", pattern)
		}
	})

	// 7.3: package.json with empty dependencies object
	t.Run("empty_dependencies_package_json", func(t *testing.T) {
		tempDir := t.TempDir()
		os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(`{"name":"test","dependencies":{}}`), 0o644)
		pattern, err := detect.DetectArchitecturePattern(tempDir)
		if err != nil {
			t.Fatalf("detection failed: %v", err)
		}
		if pattern == "" {
			t.Errorf("expected non-empty pattern")
		}
	})

	// 7.4: package.json with only devDependencies
	t.Run("dev_dependencies_only", func(t *testing.T) {
		tempDir := t.TempDir()
		os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(`{"name":"test","devDependencies":{"next":"14.0.0"}}`), 0o644)
		os.MkdirAll(filepath.Join(tempDir, "app"), 0o755)
		pattern, err := detect.DetectArchitecturePattern(tempDir)
		if err != nil {
			t.Fatalf("detection failed: %v", err)
		}
		if pattern != detect.PatternNextAppRouter {
			t.Errorf("expected nextjs_app, got %s", pattern)
		}
	})

	// 7.5: Empty directory without any files
	t.Run("empty_directory_detection", func(t *testing.T) {
		tempDir := t.TempDir()
		pattern, err := detect.DetectArchitecturePattern(tempDir)
		if err != nil {
			t.Fatalf("detection failed: %v", err)
		}
		if pattern != detect.PatternCleanArchitecture {
			t.Errorf("expected default clean_arch on empty dir, got %s", pattern)
		}
	})
}

// Boundary 8: Circular references and DAG structures (5 tests)
func TestTier2_Boundary8_CircularAndDagStructures(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 8.1: Diamond DAG (A -> B -> D, A -> C -> D)
	t.Run("diamond_dag_slicing", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export class DiamondFlow {
  stepA() { this.stepB(); this.stepC(); }
  stepB() { this.stepD(); }
  stepC() { this.stepD(); }
  stepD() { return 42; }
}`
		os.WriteFile(filepath.Join(tempDir, "DiamondFlow.ts"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "DiamondFlow.ts", "DiamondFlow.stepA", 5)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 8.2: Self-recursive function
	t.Run("self_recursive_function", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const RecurseComp = () => {
  const handleRecurse = (n: number) => {
    if (n <= 0) return 0;
    return handleRecurse(n - 1);
  };
  return <button onClick={() => handleRecurse(5)} />;
};`
		os.WriteFile(filepath.Join(tempDir, "RecurseComp.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "RecurseComp.tsx", "RecurseComp.handleRecurse", 5)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 8.3: Mutually recursive functions (A -> B -> A)
	t.Run("mutually_recursive_functions", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export class MutualCycle {
  handleA() { this.handleB(); }
  handleB() { this.handleA(); }
}`
		os.WriteFile(filepath.Join(tempDir, "MutualCycle.ts"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "MutualCycle.ts", "MutualCycle.handleA", 5)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 8.4: Tree component with child items calling parent expand
	t.Run("tree_view_nested_item_slicing", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const TreeView = () => {
  const onToggleNode = (nodeId: string) => {
    onToggleNode(nodeId + '-child');
  };
  return <div onClick={() => onToggleNode('root')} />;
};`
		os.WriteFile(filepath.Join(tempDir, "TreeView.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "TreeView.tsx", "TreeView.onToggleNode", 3)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 8.5: Deep linear chain bounding at maxDepth = 5
	t.Run("deep_linear_chain_bounds", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export class DeepChain {
  f1() { this.f2(); }
  f2() { this.f3(); }
  f3() { this.f4(); }
  f4() { this.f5(); }
  f5() { this.f6(); }
  f6() { this.f7(); }
  f7() { return true; }
}`
		os.WriteFile(filepath.Join(tempDir, "DeepChain.ts"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "DeepChain.ts", "DeepChain.f1", 5)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})
}

// Boundary 9: Mixed and unusual export formats (5 tests)
func TestTier2_Boundary9_MixedExportFormats(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	formats := []struct {
		name string
		code string
	}{
		{"export_default_const", `export default const DefaultPage = () => { const onAction = () => {}; return <div onClick={onAction}/>; };`},
		{"export_default_function", `export default function DefaultHandler() { return true; }`},
		{"export_named_clause", `const localAction = () => {}; export { localAction as customHandler };`},
		{"export_all_from", `export * from './submodule';`},
		{"mixed_named_and_default", `export const ActionA = () => {}; export default function ActionB() {}`},
	}

	for _, tc := range formats {
		t.Run("export_format_"+tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			os.WriteFile(filepath.Join(tempDir, "test.tsx"), []byte(tc.code), 0o644)
			var resp struct {
				Candidates []harvest.Candidate `json:"candidates"`
			}
			err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
			if err != nil {
				t.Fatalf("harvest failed for %s: %v", tc.name, err)
			}
		})
	}
}

// Boundary 10: Comments containing code-like strings (5 tests)
func TestTier2_Boundary10_CommentsAndCodeLikeStrings(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 10.1: Comments containing code-like strings should not produce fake candidates
	t.Run("comment_with_function_keywords_ignored", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `// Note: do not use handleLegacySubmit() here
// function processOldData() is deprecated
// Note: onAction() was removed in v2
export const RealComponent = () => {
  const handleReal = () => {};
  return <div onClick={handleReal} />;
};`
		os.WriteFile(filepath.Join(tempDir, "test.tsx"), []byte(src), 0o644)
		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		for _, c := range resp.Candidates {
			if strings.Contains(c.EntrySymbolPath, "handleLegacySubmit") || strings.Contains(c.EntrySymbolPath, "processOldData") {
				t.Errorf("comment text was harvested as candidate: %s", c.EntrySymbolPath)
			}
		}
	})

	// 10.2: Multi-line block comment with functions
	t.Run("block_comment_functions_ignored", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `/*
function executeOld() {
  return false;
}
*/
export class RealService {
  execute() { return true; }
}`
		os.WriteFile(filepath.Join(tempDir, "test.ts"), []byte(src), 0o644)
		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		for _, c := range resp.Candidates {
			if strings.Contains(c.EntrySymbolPath, "executeOld") {
				t.Errorf("block-commented function was harvested: %s", c.EntrySymbolPath)
			}
		}
	})

	// 10.3: Regex containing braces and slashes
	t.Run("regex_with_braces_preservation", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const RegexComp = () => {
  const onValidate = (input: string) => {
    const pattern = /^[a-z]{3,5}\/[0-9]+$/g;
    return pattern.test(input);
  };
  return <button onClick={() => onValidate('abc/123')} />;
};`
		os.WriteFile(filepath.Join(tempDir, "RegexComp.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "RegexComp.tsx", "RegexComp.onValidate", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 10.4: String literals containing function syntax
	t.Run("string_literals_with_function_syntax", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const StringScript = () => {
  const onRunCode = () => {
    const code = "function fake() { return 1; }";
    runner.exec(code);
  };
  return <button onClick={onRunCode} />;
};`
		os.WriteFile(filepath.Join(tempDir, "StringScript.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "StringScript.tsx", "StringScript.onRunCode", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 10.5: Template literal with nested template expressions
	t.Run("nested_template_literals", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const TemplateComp = () => {
  const onFormat = (id: string, name: string) => {
    const text = ` + "`User: ${id} - ${name ? `${name.toUpperCase()}` : 'Anonymous'}`" + `;
    return text;
  };
  return <div onClick={() => onFormat('1', 'Alice')} />;
};`
		os.WriteFile(filepath.Join(tempDir, "TemplateComp.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "TemplateComp.tsx", "TemplateComp.onFormat", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})
}

// Boundary 11: Single-expression arrow functions without braces (5 tests)
func TestTier2_Boundary11_BracelessArrowFunctions(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	cases := []struct {
		name string
		code string
	}{
		{"numeric_expression", `export const getNumber = () => 42;`},
		{"call_expression", `export const handleSend = (e) => api.send(e);`},
		{"async_call_expression", `export const fetchAsync = async () => await api.fetch();`},
		{"object_literal_expression", `export const makeConfig = () => ({ enabled: true });`},
		{"jsx_expression", `export const SimpleCard = () => <div className="card" />;`},
	}

	for _, tc := range cases {
		t.Run("braceless_"+tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			os.WriteFile(filepath.Join(tempDir, "braceless.tsx"), []byte(tc.code), 0o644)
			var resp struct {
				Candidates []harvest.Candidate `json:"candidates"`
			}
			err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
			if err != nil {
				t.Fatalf("harvest failed: %v", err)
			}
		})
	}
}

// Boundary 12: Unresolvable dynamic call targets & graceful degradation (5 tests)
func TestTier2_Boundary12_DynamicCallGracefulDegradation(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	dynamicCases := []struct {
		name string
		code string
	}{
		{"window_dynamic_method", `export const Dyn1 = () => { const onAction = () => { window[dynamicMethod](); }; return <button onClick={onAction}/>; };`},
		{"eval_invocation", `export const Dyn2 = () => { const onAction = () => { eval("console.log('test')"); }; return <button onClick={onAction}/>; };`},
		{"anonymous_iife", `export const Dyn3 = () => { const onAction = () => { ((fn) => fn())(); }; return <button onClick={onAction}/>; };`},
		{"null_assertion_call", `export const Dyn4 = () => { const onAction = () => { (null as any).doCall(); }; return <button onClick={onAction}/>; };`},
		{"unimported_external_module", `export const Dyn5 = () => { const onAction = () => { unknownLib.missingMethod(); }; return <button onClick={onAction}/>; };`},
	}

	for _, tc := range dynamicCases {
		t.Run("dynamic_"+tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			os.WriteFile(filepath.Join(tempDir, "dyn.tsx"), []byte(tc.code), 0o644)
			payload, err := sliceHelper(t, pool, ctx, tempDir, "dyn.tsx", strings.Split(tc.code, " ")[2]+".onAction", 2)
			if err != nil {
				t.Fatalf("slice crashed on %s: %v", tc.name, err)
			}
			validateContract(t, "sliced-payload.schema.json", payload)
		})
	}
}
