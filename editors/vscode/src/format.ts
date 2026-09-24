import { type CancellationToken, isCancellation } from "./cancellation";

// reportFormat runs one format request and logs it to the output channel. A count
// of 0 is not "no change": when the buffer already matched disk the server fixes
// the file in place and returns no edits, and the editor reloads it. A format
// that ran no tool is reported from the server's event stream instead.
//
// A cancelled request logs "cancelled", not a count: the client drops any result
// once the token is cancelled, and throws a CancellationError when the server
// cancelled a request the editor had not.
export async function reportFormat<R>(
  file: string,
  token: CancellationToken,
  log: (line: string) => void,
  request: () => PromiseLike<R> | R,
): Promise<R> {
  log(`format: ${file}`);
  let edits: R;
  try {
    edits = await request();
  } catch (error) {
    if (isCancellation(error)) {
      log("format: cancelled");
    }
    throw error;
  }
  log(
    token.isCancellationRequested
      ? "format: cancelled"
      : `format: ${Array.isArray(edits) ? edits.length : 0} edit(s)`,
  );
  return edits;
}
