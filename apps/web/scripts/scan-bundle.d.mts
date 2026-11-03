export interface ScanRule {
  readonly id: string;
  readonly description: string;
  readonly pattern: RegExp;
}

export interface ScanFinding {
  readonly file: string;
  readonly line: number;
  readonly rule: string;
}

export declare const RULES: readonly ScanRule[];
export declare const CLIENT_ONLY_RULES: readonly ScanRule[];
export declare function scanText(text: string, file: string, rules: readonly ScanRule[]): ScanFinding[];
export declare function scanBuildDir(buildDir: string): Promise<{ files: number; findings: ScanFinding[] }>;
