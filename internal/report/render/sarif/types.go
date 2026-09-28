package sarif

// The subset of SARIF 2.1.0 the renderer writes, in the order its fields are
// written.

// Log is a SARIF file.
type Log struct {
	Version string `json:"version"`
	Schema  string `json:"$schema"`
	Runs    []Run  `json:"runs"`
}

// Run is one tool's results in one category.
type Run struct {
	AutomationDetails AutomationDetails `json:"automationDetails"`
	// ColumnKind says columns count code points, which is what the report's
	// chars span counts.
	ColumnKind  string       `json:"columnKind"`
	Tool        Tool         `json:"tool"`
	Invocations []Invocation `json:"invocations"`
	Results     []Result     `json:"results"`
}

// AutomationDetails carries the category: the ID up to its last "/".
type AutomationDetails struct {
	ID string `json:"id"`
}

// Tool is the tool a run is of.
type Tool struct {
	Driver Driver `json:"driver"`
}

// Driver names the tool: its configured name, its app's configured version
// and official URL, and the rules its results cite.
type Driver struct {
	Name           string `json:"name"`
	Version        string `json:"version,omitempty"`
	InformationURI string `json:"informationUri,omitempty"`
	Rules          []Rule `json:"rules"`
}

// Rule is one rule a run's results cite.
type Rule struct {
	ID               string  `json:"id"`
	ShortDescription Message `json:"shortDescription"`
	HelpURI          string  `json:"helpUri,omitempty"`
}

// Message is a SARIF message with plain text.
type Message struct {
	Text string `json:"text"`
}

// Invocation is one process of the tool, or what stood in for one.
type Invocation struct {
	ExecutionSuccessful        bool                 `json:"executionSuccessful"`
	ExitCode                   *int                 `json:"exitCode,omitempty"`
	WorkingDirectory           *ArtifactLocation    `json:"workingDirectory,omitempty"`
	ToolExecutionNotifications []Notification       `json:"toolExecutionNotifications,omitempty"`
	Properties                 InvocationProperties `json:"properties"`
}

// InvocationProperties hold what the report says about an invocation.
type InvocationProperties struct {
	Datamitsu InvocationFacts `json:"datamitsu"`
}

// InvocationFacts are an invocation's identity, state and extraction outcome
// in the report.
type InvocationFacts struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Extraction string `json:"extraction"`
}

// Notification is something an invocation reported that is not a result
// at a location.
type Notification struct {
	Level      string      `json:"level"`
	Message    Message     `json:"message"`
	Descriptor *Descriptor `json:"descriptor,omitempty"`
}

// Descriptor refers to a rule by its ID.
type Descriptor struct {
	ID string `json:"id"`
}

// Result is one finding.
type Result struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Level               string            `json:"level"`
	Message             Message           `json:"message"`
	Locations           []Location        `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          ResultProperties  `json:"properties"`
}

// ResultProperties say where a finding came from and whether it failed its
// tool under the operation's failOn.
type ResultProperties struct {
	Provenance string `json:"provenance"`
	Gates      bool   `json:"gates"`
}

// Location is where a result points.
type Location struct {
	PhysicalLocation PhysicalLocation `json:"physicalLocation"`
}

// PhysicalLocation is a file and a region in it.
type PhysicalLocation struct {
	ArtifactLocation ArtifactLocation `json:"artifactLocation"`
	Region           *Region          `json:"region,omitempty"`
}

// ArtifactLocation is a URI relative to the repository root (SourceRoot), or
// an absolute file:// URI outside it.
type ArtifactLocation struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId,omitempty"`
}

// Region is 1-based; EndColumn is exclusive, and columns count code points.
type Region struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}
