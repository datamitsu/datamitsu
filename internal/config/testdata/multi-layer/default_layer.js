// Default config layer: generates .editorconfig content
export function getConfig(input) {
  return {
    ...input,
    managedConfigs: {
      ...input.managedConfigs,
      ".editorconfig": {
        content(context) {
          return "root = true\n\n[*]\nindent_style = space\nindent_size = 2\n";
        },
        scope: "git-root",
      },
    },
  };
}
