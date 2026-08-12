// Types shared with the Go backend JSON API.

export interface Property {
  key: string;
  value: string;
}

export interface DataCollection {
  id: string;
  userFacingId: string;
  displayName: string;
  description: string;
  highestRole: string;
  properties: Property[];
  gcpProject?: string;
  webUrl?: string;
}

export interface Option {
  value: string;
  label: string;
}

export interface Field {
  key: string;
  label: string;
  description: string;
  propertyKey: string;
  isDescription: boolean;
  kind: 'text' | 'plaintext' | 'shorttext' | 'tags' | 'enum';
  maxLen?: number;
  options?: Option[];
  jsonArray?: boolean;
}

export interface Resource {
  name: string;
  resourceType: string;
  bucketName?: string;
  projectId?: string;
  datasetId?: string;
}

export interface Suggestion {
  key: string;
  label: string;
  kind: 'text' | 'plaintext' | 'shorttext' | 'tags' | 'enum';
  current: string;
  suggested: string;
  tags?: string[];
  options?: Option[];
  maxLen?: number;
}

export interface ColumnInfo {
  name: string;
  type: string;
  mode?: string;
  description?: string;
}

export interface TableProfile {
  fqn: string;
  numRows: number;
  sizeBytes?: number;
  columns: ColumnInfo[];
  description?: string;
}

export interface ObjectInfo {
  name: string;
  size: number;
  contentType?: string;
}

export interface BucketProfile {
  bucket: string;
  numObjects: number;
  totalBytes: number;
  objects?: ObjectInfo[];
  truncated?: boolean;
}

export interface DataProfile {
  tables?: TableProfile[];
  buckets?: BucketProfile[];
  errors?: string[];
}

// Progress is one streamed step of the suggestion pipeline (SSE).
export interface Progress {
  phase: string;
  message: string;
  current?: number;
  total?: number;
}

// --- Deep (extensive) profiling ---

export interface ValueCount {
  value: string;
  count: number;
}

export interface ColumnStats {
  name: string;
  type: string;
  category: 'numeric' | 'temporal' | 'categorical' | 'other';
  nonNull: number;
  total: number;
  fillRate: number; // 0..1
  distinct: number;
  min?: number;
  max?: number;
  mean?: number;
  stddev?: number;
  tMin?: string;
  tMax?: string;
  topValues?: ValueCount[];
}

export interface TableStats {
  fqn: string;
  sampledRows: number;
  samplePct: number;
  columns: ColumnStats[];
}

export interface DeepProfile {
  tables: TableStats[];
  notes?: string[];
}

export interface DocLink {
  title: string;
  url: string;
}

export interface Investigation {
  dossier: string;
  links?: DocLink[];
}

export interface SaveFieldValue {
  key: string;
  value: string;
  tags?: string[];
}
