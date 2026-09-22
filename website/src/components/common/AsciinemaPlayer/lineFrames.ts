/**
 * Asciicast v2 parsing that cuts output events at line boundaries.
 *
 * Asciinema-player's frame stepping (`.` and `,`) walks the recording one output event at a time,
 * so a recording that writes a whole screen in a single event has nothing to step through. This
 * parser keeps the recorded byte stream identical — header, ANSI escape sequences and terminal
 * control sequences (`\r`, cursor movement, clear line/screen) pass through untouched — and only
 * cuts each output event after every `\n`, so `\r\n` stays attached to the line it terminates.
 * Non-output events are copied verbatim.
 *
 * The lines of one event need distinct timestamps to be steppable: the player steps to an event's
 * _time_ and then replays every event at or before it (`syncActiveSegmentToTime`), so chunks
 * sharing a timestamp are one frame no matter how many events they are. Each line is therefore
 * offset by `LINE_STEP_MS` from the one before, never past the next recorded event. Playback is
 * unaffected: the player batches output within `minFrameTime` (16.67ms by default) into a single
 * write, and a whole group is far narrower than that.
 *
 * Chunking is safe for the terminal emulator because the player feeds the chunks in order into the
 * same VT parser, and `\n` never occurs inside an escape sequence — the split never lands in the
 * middle of one.
 */

/**
 * A single asciicast event: `[time, code, data]`.
 */
export type CastEvent = [time: number, code: CastEventCode, data: string];

/**
 * Event kinds an asciicast can carry: output, input, resize, marker.
 */
export type CastEventCode = "i" | "m" | "o" | "r";

/**
 * The recording shape asciinema-player expects back from a parser.
 */
export interface CastRecording {
  cols: number;
  /**
   * Event times are in milliseconds, as the built-in parsers emit them.
   */
  events: CastEvent[];
  idleTimeLimit?: number;
  rows: number;
}

interface AsciicastV2Header {
  height?: number;
  // eslint-disable-next-line camelcase -- asciicast v2 header field name.
  idle_time_limit?: number;
  version?: number;
  width?: number;
}

// The fallbacks asciinema-player itself uses when the header reports 0.
const DEFAULT_COLS = 80;
const DEFAULT_ROWS = 24;

// Milliseconds between the lines of one output event. Small enough that even
// the longest group stays well inside the player's 16.67ms output batching
// window, so playback looks the same as before the split.
const LINE_STEP_MS = 0.1;

/**
 * An asciinema-player parser for asciicast v2 that emits one output event per terminal line. Pass
 * it as `src.parser`.
 */
export async function parseAsciicastByLines(data: unknown): Promise<CastRecording> {
  if (!(data instanceof Response)) {
    throw new TypeError("line-frame parser expects a fetched asciicast response");
  }

  const { events, header } = parseJSONLines(await data.text());

  if (header.version !== 2) {
    throw new Error(`asciicast v${String(header.version)} format not supported`);
  }

  return {
    cols: header.width === 0 || header.width === undefined ? DEFAULT_COLS : header.width,
    // Rescale to the milliseconds the player works in before splitting, so the
    // line offsets are applied in the same unit as `LINE_STEP_MS`.
    events: splitOutputEvents(
      events.map(([time, code, chunk]): CastEvent => [time * 1000, code, chunk]),
    ),
    idleTimeLimit: header.idle_time_limit,
    rows: header.height === 0 || header.height === undefined ? DEFAULT_ROWS : header.height,
  };
}

/**
 * Cut `data` after every `\n`, keeping the terminator on the chunk it ends. A trailing run without
 * a newline becomes the last chunk.
 */
export function splitAfterNewlines(data: string): string[] {
  const chunks: string[] = [];
  let start = 0;
  let newlineIndex = data.indexOf("\n");

  while (newlineIndex !== -1) {
    chunks.push(data.slice(start, newlineIndex + 1));
    start = newlineIndex + 1;
    newlineIndex = data.indexOf("\n", start);
  }

  if (start < data.length) {
    chunks.push(data.slice(start));
  }

  return chunks;
}

/**
 * Replace every multi-line output event with one event per line, each a step later than the one
 * before it. Event order is preserved and every other event is passed through unchanged. Times are
 * in milliseconds.
 */
export function splitOutputEvents(events: readonly CastEvent[]): CastEvent[] {
  const split: CastEvent[] = [];

  for (const [index, event] of events.entries()) {
    const [time, code, data] = event;

    if (code !== "o" || typeof data !== "string" || !data.includes("\n")) {
      split.push(event);
      continue;
    }

    const chunks = splitAfterNewlines(data);

    if (chunks.length < 2) {
      split.push(event);
      continue;
    }

    const step = groupStep(chunks.length, time, events[index + 1]?.[0]);

    for (const [chunkIndex, chunk] of chunks.entries()) {
      split.push([time + chunkIndex * step, code, chunk]);
    }
  }

  return split;
}

/**
 * The per-line offset for one output event, in milliseconds. The whole group has to stay strictly
 * before the next recorded event, so a gap too narrow for `LINE_STEP_MS` shrinks the step for that
 * group alone. If there is no gap at all — a following event recorded at the very same instant —
 * the group keeps one timestamp rather than displacing anything else; it is then a single frame
 * again, as it was before. No group in the recordings under `static/` hits that case.
 */
function groupStep(chunkCount: number, time: number, nextTime: number | undefined): number {
  if (nextTime === undefined) {
    return LINE_STEP_MS;
  }

  const gap = nextTime - time;

  if (gap <= 0) {
    return 0;
  }

  // Dividing by the chunk count rather than by the number of gaps between
  // chunks keeps the last line strictly before `nextTime`.
  return Math.min(LINE_STEP_MS, gap / chunkCount);
}

function parseJSONLines(text: string): { events: CastEvent[]; header: AsciicastV2Header } {
  const lines = text.split("\n");
  const header = JSON.parse(lines[0]) as AsciicastV2Header;
  const events: CastEvent[] = [];

  for (const line of lines.slice(1)) {
    // Blank trailing lines and anything that is not an event array are skipped,
    // matching the built-in asciicast parser.
    if (line[0] === "[") {
      events.push(JSON.parse(line) as CastEvent);
    }
  }

  return { events, header };
}
