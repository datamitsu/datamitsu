// Imperative ref handle (exposed via useImperativeHandle)
export interface AsciinemaPlayerHandle {
  getCurrentTime(): null | number;
  getDuration(): null | number;
  getPlayerInstance(): AsciinemaPlayerInstance | null; // Advanced: raw access
  isPaused(): boolean;
  isPlaying(): boolean;
  isReady(): boolean; // Whether player is loaded
  pause(): void;
  play(): void;
  restart(): void; // Helper: seek(0) + play()
  seek(time: number): void;
}

// Player instance interface (asciinema-player API)
export interface AsciinemaPlayerInstance {
  dispose?(): void;
  getCurrentTime(): number;
  getDuration(): number;
  isPaused(): boolean;
  isPlaying(): boolean;
  pause(): void;
  play(): void;
  seek(time: number): void;
}

// Configuration options
export interface AsciinemaPlayerOptions {
  autoPlay?: boolean;
  cols?: number;
  controls?: "auto" | boolean;
  fit?: "both" | "height" | "none" | "width";
  loop?: boolean | number;
  preload?: boolean;
  rows?: number;
  speed?: number;
  theme?: string;
}

// Component props
export interface AsciinemaPlayerProps {
  className?: string; // Optional: additional CSS classes
  onError?: (error: Error) => void; // Optional: error callback
  onLoad?: () => void; // Optional: success callback
  options?: AsciinemaPlayerOptions; // Optional: player config
  src: string; // Required: .cast file URL
}

// Loading state
export type LoadingState = "error" | "idle" | "loaded" | "loading";
