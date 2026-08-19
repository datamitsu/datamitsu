export function extractAllJSON(output: string): string[] {
  if (!output) {
    return [];
  }
  const results: string[] = [];
  let pos = 0;
  while (pos < output.length) {
    const start = output.indexOf("{", pos);
    if (start === -1) {
      break;
    }
    const end = findJSONEnd(output, start);
    if (end === -1) {
      break;
    }
    results.push(output.slice(start, end + 1));
    pos = end + 1;
  }
  return results;
}

export function extractJSON(output: string): null | string {
  if (!output) {
    return null;
  }
  const start = output.indexOf("{");
  if (start === -1) {
    return null;
  }
  const end = findJSONEnd(output, start);
  if (end === -1) {
    return null;
  }
  return output.slice(start, end + 1);
}

function findJSONEnd(output: string, start: number): number {
  let depth = 0;
  let isInString = false;
  let isEscape = false;
  for (let index = start; index < output.length; index++) {
    const ch = output[index];
    if (isEscape) {
      isEscape = false;
    } else if (isInString) {
      if (ch === "\\") {
        isEscape = true;
      } else if (ch === '"') {
        isInString = false;
      }
    } else {
      switch (ch) {
        case '"': {
          isInString = true;

          break;
        }
        case "{": {
          depth++;

          break;
        }
        case "}": {
          depth--;
          if (depth === 0) {
            return index;
          }

          break;
        }
        // No default
      }
    }
  }
  return -1;
}
