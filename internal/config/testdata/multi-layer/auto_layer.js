// Auto config layer: overrides .editorconfig with content verification
export function getConfig(input) {
  return {
    ...input,
    init: {
      ...input.init,
      ".editorconfig": {
        content: function (context) {
          // Verify upstream content exists before overriding
          if (context.existingContent && context.existingContent.indexOf("indent_style") === -1) {
            throw new Error("upstream .editorconfig missing indent_style");
          }
          // Override with custom indentation
          return "root = true\n\n[*]\nindent_style = space\nindent_size = 4\n";
        },
        scope: "git-root",
      },
    },
  };
}
