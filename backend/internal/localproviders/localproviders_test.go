package localproviders

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// clearLlamaCppOverrides makes a test see the defaults whatever the developer's
// own shell exports.
func clearLlamaCppOverrides(t *testing.T) {
	t.Helper()
	t.Setenv(EnvLlamaCppURL, "")
	t.Setenv(EnvLlamaCppPort, "")
}

func TestBuiltinCoversOllamaAndLlamaCpp(t *testing.T) {
	clearLlamaCppOverrides(t)
	byName := map[string]Endpoint{}
	for _, endpoint := range Builtin() {
		byName[endpoint.Name] = endpoint
	}
	ollama, ok := byName["ollama"]
	if !ok || ollama.Kind != KindOllama || ollama.BaseURL != DefaultOllamaBaseURL {
		t.Errorf("ollama built-in = %+v, want its native shape on 11434", ollama)
	}
	llama, ok := byName["llama.cpp"]
	if !ok || llama.Kind != KindOpenAICompatible || llama.BaseURL != DefaultLlamaCppBaseURL() {
		t.Errorf("llama.cpp built-in = %+v, want the OpenAI-compatible shape on %s", llama, DefaultLlamaCppBaseURL())
	}
	// Loopback only: probing anything else unasked would reach across the network.
	for _, endpoint := range Builtin() {
		if !endpoint.Builtin {
			t.Errorf("%s is not marked built-in", endpoint.Name)
		}
	}
}

// The one place the shipped defaults are spelled out: every other test, the
// HTTP presets and the built-ins derive them, so changing a default is a change
// here and to its constant, and nowhere else.
func TestDefaultAddressesAreTheEnginesOwnDefaults(t *testing.T) {
	if DefaultOllamaBaseURL != "http://127.0.0.1:11434" {
		t.Errorf("DefaultOllamaBaseURL = %q", DefaultOllamaBaseURL)
	}
	if got := DefaultLlamaCppBaseURL(); got != "http://127.0.0.1:8080" {
		t.Errorf("DefaultLlamaCppBaseURL() = %q", got)
	}
}

func TestResolveLlamaCppBaseURL(t *testing.T) {
	def := DefaultLlamaCppBaseURL()
	cases := []struct {
		name, url, port string
		want            string
		// problems are substrings the reported problem must contain; none
		// means the overrides must be accepted silently.
		problems []string
	}{
		{name: "nothing set", want: def},
		{name: "blank values are unset", url: "  ", port: "\t", want: def},
		{name: "port moves loopback", port: "9090", want: "http://127.0.0.1:9090"},
		{name: "port is trimmed", port: " 9090 ", want: "http://127.0.0.1:9090"},
		{name: "url replaces the address", url: "http://localhost:9191", want: "http://localhost:9191"},
		{name: "url is normalised like an added endpoint", url: " http://127.0.0.1:9191/ ", want: "http://127.0.0.1:9191"},
		{name: "url may name another host and a path", url: "https://gpu-box.lan:8443/llama", want: "https://gpu-box.lan:8443/llama"},
		{name: "url wins over port", url: "http://127.0.0.1:9191", port: "9090", want: "http://127.0.0.1:9191"},
		{name: "url wins even over a bad port", url: "http://127.0.0.1:9191", port: "nope", want: "http://127.0.0.1:9191"},
		{name: "bad url falls back to port", url: "127.0.0.1:9191", port: "9090", want: "http://127.0.0.1:9090", problems: []string{EnvLlamaCppURL}},
		{name: "bad url falls back to default", url: "ftp://127.0.0.1:9191", want: def, problems: []string{EnvLlamaCppURL}},
		{name: "url with a query is refused", url: "http://127.0.0.1:9191/?key=x", want: def, problems: []string{EnvLlamaCppURL}},
		{name: "non-numeric port", port: "eighty", want: def, problems: []string{EnvLlamaCppPort}},
		{name: "port zero", port: "0", want: def, problems: []string{EnvLlamaCppPort}},
		{name: "port out of range", port: "65536", want: def, problems: []string{EnvLlamaCppPort}},
		{name: "negative port", port: "-1", want: def, problems: []string{EnvLlamaCppPort}},
		{name: "both bad reports both", url: "nope", port: "nope", want: def, problems: []string{EnvLlamaCppURL, EnvLlamaCppPort}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{EnvLlamaCppURL: tc.url, EnvLlamaCppPort: tc.port}
			got, err := resolveLlamaCppBaseURL(func(name string) string { return env[name] })
			if got != tc.want {
				t.Errorf("base URL = %q, want %q", got, tc.want)
			}
			if len(tc.problems) == 0 {
				if err != nil {
					t.Errorf("unexpected problem: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("no problem reported, want one naming %v", tc.problems)
			}
			for _, want := range tc.problems {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("problem %q does not name %s", err, want)
				}
			}
		})
	}
}

// The override has to reach everything that names the llama.cpp address: the
// probed built-in, the Settings preset, and the endpoint list a link resolves
// against.
func TestLlamaCppOverrideReachesBuiltinPresetAndAll(t *testing.T) {
	llamaIn := func(endpoints []Endpoint) string {
		for _, endpoint := range endpoints {
			if endpoint.Name == "llama.cpp" {
				return endpoint.BaseURL
			}
		}
		return ""
	}
	check := func(t *testing.T, want string) {
		t.Helper()
		if got := LlamaCppBaseURL(); got != want {
			t.Errorf("LlamaCppBaseURL() = %q, want %q", got, want)
		}
		if got := llamaIn(Builtin()); got != want {
			t.Errorf("built-in = %q, want %q", got, want)
		}
		if got := llamaIn(Presets()); got != want {
			t.Errorf("preset = %q, want %q", got, want)
		}
		if got := llamaIn(All(t.TempDir())); got != want {
			t.Errorf("All = %q, want %q", got, want)
		}
	}

	t.Run("port", func(t *testing.T) {
		clearLlamaCppOverrides(t)
		t.Setenv(EnvLlamaCppPort, "9090")
		check(t, "http://127.0.0.1:9090")
		if err := LlamaCppOverrideProblem(); err != nil {
			t.Errorf("problem = %v", err)
		}
	})
	t.Run("url", func(t *testing.T) {
		clearLlamaCppOverrides(t)
		t.Setenv(EnvLlamaCppPort, "9090")
		t.Setenv(EnvLlamaCppURL, "http://localhost:9191/")
		check(t, "http://localhost:9191")
	})
	t.Run("unusable override keeps the default and is reported", func(t *testing.T) {
		clearLlamaCppOverrides(t)
		t.Setenv(EnvLlamaCppPort, "not-a-port")
		check(t, DefaultLlamaCppBaseURL())
		if err := LlamaCppOverrideProblem(); err == nil {
			t.Error("an unusable port was not reported")
		}
	})
}

// A stored endpoint at the overridden address is the built-in under another
// name, and is listed once, exactly as one at the default address is.
func TestAllDropsStoredEndpointAtOverriddenLlamaCppAddress(t *testing.T) {
	clearLlamaCppOverrides(t)
	dir := t.TempDir()
	if _, err := Add(dir, Endpoint{Name: "mine", BaseURL: "http://127.0.0.1:9090"}); err != nil {
		t.Fatal(err)
	}
	if got := len(All(dir)); got != 3 {
		t.Fatalf("endpoints before override = %d, want the 2 built-ins and the stored one", got)
	}
	t.Setenv(EnvLlamaCppPort, "9090")
	all := All(dir)
	if len(all) != 2 {
		t.Fatalf("endpoints after override = %+v, want the stored duplicate dropped", all)
	}
	for _, endpoint := range all {
		if !endpoint.Builtin {
			t.Errorf("%s survived as a duplicate of the built-in", endpoint.Name)
		}
	}
}

func TestPresetsAreTheBuiltinsPlusBionic(t *testing.T) {
	clearLlamaCppOverrides(t)
	presets := Presets()
	builtin := Builtin()
	if len(presets) != len(builtin)+1 {
		t.Fatalf("presets = %+v", presets)
	}
	for i, endpoint := range builtin {
		if presets[i].Name != endpoint.Name || presets[i].BaseURL != endpoint.BaseURL || presets[i].Kind != endpoint.Kind {
			t.Errorf("preset %d = %+v, want built-in %+v", i, presets[i], endpoint)
		}
	}
	if last := presets[len(presets)-1]; last.Name != "bionic" || last.Kind != KindOpenAICompatible || last.BaseURL != "http://127.0.0.1:1234" {
		t.Errorf("last preset = %+v, want bionic on 1234", last)
	}
}

// Builtin must hand out a copy, or a caller editing the slice would change what
// every later probe looks at.
func TestBuiltinCannotBeMutatedByCallers(t *testing.T) {
	Builtin()[0].BaseURL = "http://evil.example"
	if Builtin()[0].BaseURL == "http://evil.example" {
		t.Fatal("Builtin handed out its own slice")
	}
}

func TestProbeReadsAnOpenAICompatibleModelList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("asked for %s, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen2.5-coder"},{"id":"llama3.1"},{"id":"  "}]}`))
	}))
	defer server.Close()

	result := Probe(context.Background(), server.Client(), Endpoint{
		Name: "local", BaseURL: server.URL, Kind: KindOpenAICompatible,
	})
	if !result.Reachable {
		t.Fatalf("not reachable: %s", result.Error)
	}
	// Sorted, and a blank id is not a model.
	if want := []string{"llama3.1", "qwen2.5-coder"}; !reflect.DeepEqual(result.Models, want) {
		t.Errorf("models = %v, want %v", result.Models, want)
	}
}

func TestProbeReadsOllamaTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("asked for %s, want /api/tags", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:latest","model":"llama3.2:latest"},{"model":"only-model-field"}]}`))
	}))
	defer server.Close()

	result := Probe(context.Background(), server.Client(), Endpoint{
		Name: "ollama", BaseURL: server.URL, Kind: KindOllama,
	})
	if !result.Reachable {
		t.Fatalf("not reachable: %s", result.Error)
	}
	if want := []string{"llama3.2:latest", "only-model-field"}; !reflect.DeepEqual(result.Models, want) {
		t.Errorf("models = %v, want %v", result.Models, want)
	}
}

// A server that is up with nothing pulled is reachable, not an error.
func TestProbeTreatsAnEmptyCatalogAsReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	result := Probe(context.Background(), server.Client(), Endpoint{BaseURL: server.URL, Kind: KindOpenAICompatible})
	if !result.Reachable || len(result.Models) != 0 || result.Error != "" {
		t.Fatalf("result = %+v, want reachable with no models and no error", result)
	}
}

func TestProbeReportsWhyAnEndpointDidNotAnswer(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer refusing.Close()
	if result := Probe(context.Background(), refusing.Client(), Endpoint{BaseURL: refusing.URL, Kind: KindOpenAICompatible}); result.Reachable || result.Error == "" {
		t.Errorf("a 404 was treated as reachable: %+v", result)
	}

	// A URL pointing at a server of another shape is reported, not hidden: this
	// is how a wrong --kind shows up.
	wrongShape := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	defer wrongShape.Close()
	if result := Probe(context.Background(), wrongShape.Client(), Endpoint{BaseURL: wrongShape.URL, Kind: KindOpenAICompatible}); result.Reachable || result.Error == "" {
		t.Errorf("a non-JSON answer was treated as reachable: %+v", result)
	}
}

func TestProbeDoesNotReachAClosedPort(t *testing.T) {
	// A closed loopback port is the ordinary "engine not running" case.
	result := Probe(context.Background(), &http.Client{Timeout: DefaultTimeout}, Endpoint{
		Name: "nothing", BaseURL: "http://127.0.0.1:1", Kind: KindOpenAICompatible,
	})
	if result.Reachable {
		t.Fatal("probing a closed port reported an endpoint that is up")
	}
	if result.Error == "" {
		t.Fatal("an unreachable endpoint must say why")
	}
}

func TestValidateBaseURLRejectsWhatCannotBeProbed(t *testing.T) {
	for _, bad := range []string{"", "   ", "127.0.0.1:11434", "ftp://host", "http://", "http://host?x=1", "http://host#frag"} {
		if _, err := ValidateBaseURL(bad); err == nil {
			t.Errorf("ValidateBaseURL(%q) was accepted", bad)
		}
	}
	// A trailing slash is dropped so the probe never builds a double slash.
	if got, err := ValidateBaseURL("http://127.0.0.1:8080/"); err != nil || got != "http://127.0.0.1:8080" {
		t.Errorf("ValidateBaseURL trailing slash = (%q, %v)", got, err)
	}
	// A path is kept: a gateway may serve its API under one.
	if got, err := ValidateBaseURL("https://host/openai"); err != nil || got != "https://host/openai" {
		t.Errorf("ValidateBaseURL with a path = (%q, %v)", got, err)
	}
}

func TestValidateNameProtectsTheBuiltins(t *testing.T) {
	for _, bad := range []string{"", "  ", "my endpoint", "a/b", "a\\b", "ollama", "Ollama", "llama.cpp"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) was accepted", bad)
		}
	}
	if err := ValidateName("bionic"); err != nil {
		t.Errorf("ValidateName(bionic) = %v, want it accepted", err)
	}
}

func TestValidateKindDefaultsToOpenAICompatible(t *testing.T) {
	if got, err := ValidateKind(""); err != nil || got != KindOpenAICompatible {
		t.Errorf("ValidateKind(\"\") = (%q, %v)", got, err)
	}
	if got, err := ValidateKind("ollama"); err != nil || got != KindOllama {
		t.Errorf("ValidateKind(ollama) = (%q, %v)", got, err)
	}
	if _, err := ValidateKind("anthropic"); err == nil {
		t.Error("ValidateKind accepted an unknown shape")
	}
}

func TestAddLoadRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := Load(dir); len(got) != 0 {
		t.Fatalf("a fresh data dir has endpoints: %+v", got)
	}

	// A vendor's own local server, named: the generic shape, not a special case.
	if _, err := Add(dir, Endpoint{Name: "bionic", BaseURL: "http://127.0.0.1:9000/"}); err != nil {
		t.Fatalf("add bionic: %v", err)
	}
	if _, err := Add(dir, Endpoint{Name: "studio", BaseURL: "http://127.0.0.1:1234", Kind: KindOpenAICompatible}); err != nil {
		t.Fatalf("add studio: %v", err)
	}
	loaded := Load(dir)
	if len(loaded) != 2 {
		t.Fatalf("loaded = %+v, want two endpoints", loaded)
	}
	// Stored normalized: trailing slash gone, kind defaulted, never built-in.
	byName := map[string]Endpoint{}
	for _, endpoint := range loaded {
		byName[endpoint.Name] = endpoint
	}
	if got := byName["bionic"]; got.BaseURL != "http://127.0.0.1:9000" || got.Kind != KindOpenAICompatible || got.Builtin {
		t.Errorf("bionic stored as %+v", got)
	}

	// Adding the same name again replaces it rather than making a second entry.
	if _, err := Add(dir, Endpoint{Name: "bionic", BaseURL: "http://127.0.0.1:9100", Kind: KindOllama}); err != nil {
		t.Fatalf("re-add bionic: %v", err)
	}
	loaded = Load(dir)
	if len(loaded) != 2 {
		t.Fatalf("re-adding a name grew the store: %+v", loaded)
	}
	for _, endpoint := range loaded {
		if endpoint.Name == "bionic" && (endpoint.BaseURL != "http://127.0.0.1:9100" || endpoint.Kind != KindOllama) {
			t.Errorf("re-added bionic = %+v, want the new URL and kind", endpoint)
		}
	}

	if err := Remove(dir, "BIONIC"); err != nil {
		t.Fatalf("remove is case-insensitive: %v", err)
	}
	if got := Load(dir); len(got) != 1 || got[0].Name != "studio" {
		t.Fatalf("after remove = %+v, want only studio", got)
	}
	// Removing a name that is not there is an error, not a silent success.
	if err := Remove(dir, "bionic"); err == nil {
		t.Error("removing an absent endpoint succeeded")
	}
}

func TestAddRefusesWhatLoadWouldHaveToDrop(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []Endpoint{
		{Name: "ollama", BaseURL: "http://127.0.0.1:9000"},
		{Name: "bad name", BaseURL: "http://127.0.0.1:9000"},
		{Name: "ok", BaseURL: "not-a-url"},
		{Name: "ok", BaseURL: "http://127.0.0.1:9000", Kind: Kind("nonsense")},
	} {
		if _, err := Add(dir, bad); err == nil {
			t.Errorf("Add(%+v) was accepted", bad)
		}
	}
	if got := Load(dir); len(got) != 0 {
		t.Fatalf("a refused add still wrote something: %+v", got)
	}
}

func TestLoadSkipsEntriesItCannotUse(t *testing.T) {
	dir := t.TempDir()
	// A hand-edited store must not introduce an endpoint the commands could not
	// address, nor a duplicate shadowing the first entry.
	body := `[
		{"name":"good","baseUrl":"http://127.0.0.1:9000","kind":"openai-compatible"},
		{"name":"bad name","baseUrl":"http://127.0.0.1:9001","kind":"openai-compatible"},
		{"name":"ollama","baseUrl":"http://127.0.0.1:9002","kind":"ollama"},
		{"name":"nourl","baseUrl":"","kind":"ollama"},
		{"name":"badkind","baseUrl":"http://127.0.0.1:9003","kind":"nonsense"},
		{"name":"good","baseUrl":"http://127.0.0.1:9004","kind":"ollama"}
	]`
	if err := os.WriteFile(filepath.Join(dir, storeName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := Load(dir)
	if len(loaded) != 1 || loaded[0].Name != "good" || loaded[0].BaseURL != "http://127.0.0.1:9000" {
		t.Fatalf("loaded = %+v, want only the first usable entry", loaded)
	}
}

func TestLoadTreatsAnUnreadableStoreAsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, storeName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Discovery still has its built-ins to report, so a broken file is not fatal.
	if got := Load(dir); len(got) != 0 {
		t.Fatalf("loaded = %+v, want none", got)
	}
	if len(All(dir)) != len(Builtin()) {
		t.Fatal("a broken store cost the built-ins")
	}
}

func TestAllPutsBuiltinsFirstAndDropsADuplicateAddress(t *testing.T) {
	dir := t.TempDir()
	// Same address as the ollama built-in under another name: probing it twice
	// would list one engine as two endpoints.
	if _, err := Add(dir, Endpoint{Name: "mine", BaseURL: "http://127.0.0.1:11434", Kind: KindOllama}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, Endpoint{Name: "other", BaseURL: "http://127.0.0.1:9000"}); err != nil {
		t.Fatal(err)
	}
	all := All(dir)
	names := make([]string, 0, len(all))
	for _, endpoint := range all {
		names = append(names, endpoint.Name)
	}
	if want := []string{"ollama", "llama.cpp", "other"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("All = %v, want %v", names, want)
	}
}

func TestProbeAllKeepsTheInputOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	endpoints := []Endpoint{
		{Name: "first", BaseURL: server.URL, Kind: KindOpenAICompatible},
		{Name: "second", BaseURL: "http://127.0.0.1:1", Kind: KindOpenAICompatible},
		{Name: "third", BaseURL: server.URL, Kind: KindOpenAICompatible},
	}
	results := ProbeAll(context.Background(), server.Client(), endpoints)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	for i, want := range []string{"first", "second", "third"} {
		if results[i].Name != want {
			t.Fatalf("result %d = %s, want %s", i, results[i].Name, want)
		}
	}
}
