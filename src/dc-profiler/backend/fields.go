package main

// Field describes a single catalog attribute we can auto-fill on a data
// collection. Each field maps to either the built-in workspace `description`
// or a workspace property key (terra-dc-* / terra-* format).
type Field struct {
	// Key is the stable identifier used in the API/UI.
	Key string `json:"key"`
	// Label is the human-facing name shown in the UI.
	Label string `json:"label"`
	// Description explains to the LLM (and the user) what the field is for.
	Description string `json:"description"`
	// PropertyKey is the WSM workspace property key to write to. Empty when the
	// field targets the built-in workspace `description` field (see IsDescription).
	PropertyKey string `json:"propertyKey"`
	// IsDescription marks the built-in workspace description field, saved via
	// PATCH /workspaces/v1/{id} rather than a property.
	IsDescription bool `json:"isDescription"`
	// Kind controls how the value is rendered/edited and generated.
	//   "text"      - free text / markdown, multi-line
	//   "plaintext" - multi-line plain text (no markdown; stored/shown verbatim)
	//   "shorttext" - short single-line text (has MaxLen)
	//   "tags"      - a set of enum slugs (see Options), stored JSON-encoded
	//   "enum"      - a single enum slug (see Options)
	Kind string `json:"kind"`
	// MaxLen, when > 0, is the maximum length the generator/UI should respect.
	MaxLen int `json:"maxLen,omitempty"`
	// Options lists the allowed slugs for "tags"/"enum" fields.
	Options []Option `json:"options,omitempty"`
	// JSONArray indicates the property value is a JSON-encoded array of slugs.
	JSONArray bool `json:"jsonArray,omitempty"`
}

// Option is a selectable value for tag/enum fields.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var therapeuticTagOptions = []Option{
	{"cardiology", "Cardiology"},
	{"dermatology", "Dermatology"},
	{"endocrinology", "Endocrinology"},
	{"gastroenterology", "Gastroenterology"},
	{"immunology", "Immunology"},
	{"infectious-diseases", "Infectious Diseases"},
	{"neurology", "Neurology"},
	{"oncology", "Oncology"},
	{"orthopedics", "Orthopedics"},
	{"respiratory", "Respiratory"},
	{"ta-agnostic", "TA-agnostic"},
	{"observational-health", "Observational health"},
	{"general-health", "General health"},
	{"mental-health", "Mental health"},
}

var dataModalityTagOptions = []Option{
	{"claims", "Claims"},
	{"ehr", "EHR"},
	{"genomics", "Genomics [WGS, exome, RNA-seq, single-cell, Microarray]"},
	{"imaging", "Imaging"},
	{"microbiomics", "Microbiomics"},
	{"patient-reported-outcomes-survey", "Patient Reported Outcomes / Survey"},
	{"proteomics", "Proteomics"},
	{"epigenomics", "Epigenomics"},
	{"social-determinants-of-health", "Social Determinants of Health"},
	{"transcriptomics", "Transcriptomics"},
	{"epro", "ePRO"},
	{"ecrf", "eCRF"},
	{"lab-results", "Lab results"},
	{"sensors", "Sensors"},
	{"simulated-dialogue", "Simulated dialogue"},
}

var updateFrequencyOptions = []Option{
	{"daily", "Daily"},
	{"weekly", "Weekly"},
	{"bi-weekly", "Bi-weekly"},
	{"monthly", "Monthly"},
	{"quarterly", "Quarterly"},
	{"yearly", "Yearly"},
}

// Fields is the ordered catalog of attributes the app can suggest and save.
var Fields = []Field{
	{
		Key:           "description",
		Label:         "Description",
		Description:   "A comprehensive, markdown-formatted description of the data collection: what it contains, its provenance, how it is organized, and who would use it.",
		IsDescription: true,
		Kind:          "text",
	},
	{
		Key:         "summary",
		Label:       "Short summary",
		Description: "A one-sentence summary (<=140 chars) of the data collection, shown in cards and search results.",
		PropertyKey: "terra-workspace-short-description",
		Kind:        "shorttext",
		MaxLen:      140,
	},
	{
		Key:         "dataSnapshot",
		Label:       "Data snapshot",
		Description: "A concise overview of the concrete data assets: the BigQuery tables and GCS buckets/objects, their row/object counts and sizes, so a user knows at a glance what is inside.",
		PropertyKey: "terra-dc-data-snapshot",
		Kind:        "text",
	},
	{
		Key:         "dataDictionary",
		Label:       "Data dictionary",
		Description: "A field-by-field data dictionary / schema. For each table, add a `### table` heading followed by a GFM markdown table with columns: Column, Type, Description — one row per column.",
		PropertyKey: "terra-dc-data-dictionary",
		Kind:        "text",
	},
	{
		Key:         "dataModel",
		Label:       "Data model",
		Description: "How the tables relate to one another: primary/foreign keys, grain of each table, and the overall data model. Plain text only (no markdown), kept concise.",
		PropertyKey: "terra-dc-data-model",
		Kind:        "plaintext",
	},
	{
		Key:         "sampleUseCases",
		Label:       "Sample use cases",
		Description: "A few realistic analytical use cases or research questions this data collection can answer.",
		PropertyKey: "terra-dc-usage-examples-sample-use-cases",
		Kind:        "text",
	},
	{
		Key:         "sampleQueries",
		Label:       "Sample SQL queries",
		Description: "2-4 ready-to-run, correct BigQuery SQL queries against the actual tables (use fully-qualified `project.dataset.table` names). Put EACH query in its own fenced ```sql code block with a leading `-- comment` describing what it does.",
		PropertyKey: "terra-dc-usage-examples-sample-queries",
		Kind:        "text",
	},
	{
		Key:         "references",
		Label:       "References",
		Description: "Links or citations to related publications, documentation, or source datasets.",
		PropertyKey: "terra-dc-usage-examples-references",
		Kind:        "text",
	},
	{
		Key:         "externalDocumentation",
		Label:       "External documentation",
		Description: "Links to authoritative external documentation about the source data (e.g. the originating agency's data portal or methodology pages).",
		PropertyKey: "terra-dc-external-documentation",
		Kind:        "text",
	},
	{
		Key:         "geographicCoverage",
		Label:       "Geographic coverage",
		Description: "The geographic scope of the data (countries, regions), inferred from the data where possible.",
		PropertyKey: "terra-dc-geographic-coverage",
		Kind:        "shorttext",
	},
	{
		Key:         "timeFrame",
		Label:       "Time frame",
		Description: "The time period covered by the data (e.g. 1990-2022), inferred from date/year columns where possible.",
		PropertyKey: "terra-dc-time-frame",
		Kind:        "shorttext",
	},
	{
		Key:         "updateFrequency",
		Label:       "Update frequency",
		Description: "How often the source data is refreshed. Choose the single best-matching option.",
		PropertyKey: "terra-update-frequency",
		Kind:        "enum",
		Options:     updateFrequencyOptions,
	},
	{
		Key:         "therapeuticTags",
		Label:       "Therapeutic area tags",
		Description: "The therapeutic area(s) this data is relevant to. Choose only from the allowed options; may be empty if none apply.",
		PropertyKey: "terra-therapeutic-tags",
		Kind:        "tags",
		Options:     therapeuticTagOptions,
		JSONArray:   true,
	},
	{
		Key:         "dataModalityTags",
		Label:       "Data modality tags",
		Description: "The data modality/modalities present. Choose only from the allowed options; may be empty if none apply.",
		PropertyKey: "terra-data-modality-tags",
		Kind:        "tags",
		Options:     dataModalityTagOptions,
		JSONArray:   true,
	},
}

// FieldByKey returns the catalog field for a key, or nil.
func FieldByKey(key string) *Field {
	for i := range Fields {
		if Fields[i].Key == key {
			return &Fields[i]
		}
	}
	return nil
}
