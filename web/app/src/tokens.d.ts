// The desktop's design tokens (web/tokens.json, generated from cmd/deneb by
// go run ./web/tokens/gen); the build ships them as tokens.js.
export interface SpringOptions {
  duration: number; // ms, as MyGo's SpringOptions.Duration
  bounce: number;
}

declare const tokens: {
  generatedBy: string;
  color: {
    light: Record<string, string>;
    dark: Record<string, string>;
    owners: string[];
    white?: string;
    black?: string;
  };
  theme: { fontSize: number };
  desktop: Record<string, Record<string, unknown>>;
  spring: { Smooth: SpringOptions; Snappy: SpringOptions; Bouncy: SpringOptions };
};
export default tokens;
