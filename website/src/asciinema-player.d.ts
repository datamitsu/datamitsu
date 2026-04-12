declare module "asciinema-player" {
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

  export interface AsciinemaPlayerOptions {
    autoPlay?: boolean;
    cols?: number;
    controls?: "auto" | boolean;
    fit?: "both" | "height" | "none" | "width";
    idleTimeLimit?: number;
    loop?: boolean | number;
    poster?: string;
    preload?: boolean;
    rows?: number;
    speed?: number;
    startAt?: number | string;
    terminalFontFamily?: string;
    terminalFontSize?: string;
    theme?: string;
  }

  export function create(
    src: string,
    element: HTMLElement,
    options?: AsciinemaPlayerOptions,
  ): AsciinemaPlayerInstance;
}

declare module "asciinema-player/dist/bundle/asciinema-player.css";
