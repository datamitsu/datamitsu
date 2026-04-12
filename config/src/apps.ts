/// <reference path="../config.d.ts" />

import fnmApps from "./fnmApps.json";
import githubApps from "./githubApps.json";
import uvApps from "./uvApps.json";

interface AppStateGithub {
  binaries: BinManager.MapOfBinaries;
  configHash: string;
}

export const mapOfApps: BinManager.MapOfApps = {
  echo: {
    shell: {
      args: ["echo", `"Hello from shell"`],
      name: "echo",
    },
  },
  jscowsay: {
    // https://www.npmjs.com/package/cowsay
    description: fnmApps.jscowsay.description,
    fnm: {
      binPath: "node_modules/.bin/cowsay",
      lockFile:
        "br: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",
      packageName: fnmApps.jscowsay.packageName,
      version: fnmApps.jscowsay.version,
    },
  },
  ktlint: {
    jvm: {
      jarHash: "a3fd620207d5c40da6ca789b95e7f823c54e854b7fade7f613e91096a3706d75",
      jarUrl: "https://github.com/pinterest/ktlint/releases/download/1.8.0/ktlint",
      version: "1.8.0",
    },
  },
  pycowsay: {
    // https://pypi.org/project/pycowsay/
    uv: {
      lockFile:
        "br:G2QDAJwFOdlpKrSd3JPix2LKXhxBm3P5GNWLbjsEZR4HRA2iwLI1emw5MmU79TddCBA45/gyciqw9l3rx3fxHy22wI13G09HMLzIU6dTUPxkh3Zp0o0eQlXUOiQkYOex/pjz63fR9xQuyd/gD6by+YBXyACCcZpinpZOJ1G3TcGBYcoo8pbD3IMnez1mrEGOHigreRQVyUS8IoSQiw8cOeqhfQBouLxJ5oDA4kNbjYtphulDEKfOhBrvqZ4n8vaAjJ6PuIGS1lw+0LxBHXvrZ1a1/uiLuYmMHaSVnRgC22YZa3v4RZJIyNHv98C9sXTwE0Ko/UTjFwU67coWeZfThfOJcwg7saoSO4Ho6LKYquM00WLFaV2C+wl6WKYhw+dg5NmJxXuvBKc8kNX+RyJu6dW0IFORYqc1PfSWIzFtiMa6iZuNLRg8pBlq51Sn6rceLU8xIZ8HUhA+thMH4JlJKISxIBnPQ4oOzpWINVKIp00modWZyE7Cu6b26lvmsAzBbN9Qq8v6Q/lLHzewoD34lteh+DuNcMYz+prZtgTOTGK1Ktb7zlFmF49e+dgLfbY6uKEcbalQLLoqMmYv",
      packageName: uvApps.pycowsay.packageName,
      version: uvApps.pycowsay.version,
    },
  },
  task: {
    binary: {
      binaries: githubApps.binaries.task.binaries as unknown as Record<string, AppStateGithub>,
      version: githubApps.apps.task.tag,
    },
  },
};
