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
  folderId?: string;
  // versionId is the DC version (top-level folder) the resource belongs to, or
  // "__none__" for resources at the workspace root.
  versionId?: string;
  // folderPath is the human-readable folder chain (e.g. "Version 1/raw/2023").
  folderPath?: string;
}

// Version is a DC version (a top-level folder) for the profiling picker.
export interface Version {
  id: string;
  name: string;
  isPublished: boolean;
  publishedDate?: string;
  isDefault: boolean;
  resourceCount: number;
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
  folderPath?: string;
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
  folderPath?: string;
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
  p25?: number;
  median?: number;
  p75?: number;
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
