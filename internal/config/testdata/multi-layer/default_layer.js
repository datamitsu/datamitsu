// Default config layer: generates .editorconfig content
export function getConfig(input) {
  return {
    ...input,
    setup: {
      ...input.setup,
      ".editorconfig": {
        content: function (context) {
          return "root = true\n\n[*]\nindent_style = space\nindent_size = 2\n";
        },
        scope: "git-root",
      },
    },
  };
}
