/// <reference path="../config.d.ts" />

import githubApps from "./githubApps.json";
import nodeApps from "./nodeApps.json";
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
  govulncheck: {
    // https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck
    go: {
      lockFile:
        "br:G0sFQBwHbuzETnD8ym6qTez1zeTVDhHEWieOau3QEAK7iF/qsPSFyNFNziF8hK45cEzhtcQjLNBANlGggWz417NnsEgNFpGvlIvpIv9XMNl6JUD/YPXqu2H/hONQAkf6f4Pf4Ak32AGnD0SlXz3YB7N/wg0lYzFmPlYw2YzogaAO6OZ43LxAYIpHht0C4oyGo0KXUEhC6f97bvbtMp5owv4LcBYoiWMYTeEUtSc+4WEWRnRGoqmUDWE5KR4Sdqh7M2IH5w738TfY1g3Vlj2vHPvsTtiFJpiU1p5IEOFi62YP3QYPPNEN6aQvXF+3xzwMhVT+9htoBccnPHSA5JbIfXoZOc+DGztpFUJy0KIl9MZhusuFCaHKiWXMZH/zS5vykIDYk05Rzaojpi5U9s71VocnOl34IXVy/VIePJRkLjE81nyvkmTdu9JPc6tMiZTNZPEWI2UMP91qiR6vCpOO4EoSiqIIOi7dCAQlewygPFmjDJ+414c3a66xT5PJyl4pTNYIc5ZrbTpd4XLr5OVsKbxWtkxJ3iXsSvF2juS9qNpXNXxlWd5yabN0mXN/1Rd/5U4NOFcT7Zw8mOxPU72nQjAynTdFyI0RcxbET6OAMMGRLke+oiIvh1QN4dF9k13enu9HYc2XK+AG+qiUn8rLey3c2kCiSszm9GUM5qXs5IhBn1fKbwOoPXlbI8PiofHxIBcWdm/Wi2uUYWlfoY8MKwxEkKtFxGsXPCbr4JWKHMplzrU1k9kI+E51XnU0Eq2eO54FSpu2c3F+JFiXXniyVxDsyeaBvxrsYOR+fjE9lk0QErXcSOSciTnxo+FWON32V8AWk1bWppR5H9dCA1sovOvXQCaGxymCNpc4wmg6ckkBjVD1dtLDK8Z2",
      packageName: "golang.org/x/vuln/cmd/govulncheck",
      version: "v1.3.0",
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
  "semver-bun": {
    bun: {
      binPath: "node_modules/semver/bin/semver.js",
      lockFile:
        "br:G6QBABwHdiz4goGDjYmgyaWBefalYh5i1AA1iAva3FIA2Z3v77+0zKMcqJWDkkvt9GmU53F8D51bo1hh+/iRiAKKQO6Yl1SCMkGBwDRFDPEhLT5pWpz8d1GBNg2Nf01bXsSBtwPEiiVAiVQtThSIGCbpP0tLvIJrXq7FHmIyZIuVYiLBBJGdNa9zigKSG4UN3qHCo2y2NP3HSitlYn54msEvC2pRoPGRH+91eT+uPpfXmhs/qLtr8+5+FT3K8n81rMfXKq/TVzD3vBdpO9Q02xQcyl6jOHgf5UdwN9HVL9tYeZ5uNG/c73dAZvaysmmpRQkO+jQFnmmfZGL5ag73P+Glu1HRAKEDAA==",
      packageName: nodeApps.semver.packageName,
      version: nodeApps.semver.version,
    },
    // https://www.npmjs.com/package/semver
    description: nodeApps.semver.description,
  },
  "semver-node": {
    // https://www.npmjs.com/package/semver
    description: nodeApps.semver.description,
    node: {
      binPath: "node_modules/.bin/semver",
      lockFile:
        "br:G6QBABwHdiz4goGDjYmgyaWBefalYh5i1AA1iAva3FIA2Z3v77+0zKMcqJWDkkvt9GmU53F8D51bo1hh+/iRiAKKQO6Yl1SCMkGBwDRFDPEhLT5pWpz8d1GBNg2Nf01bXsSBtwPEiiVAiVQtThSIGCbpP0tLvIJrXq7FHmIyZIuVYiLBBJGdNa9zigKSG4UN3qHCo2y2NP3HSitlYn54msEvC2pRoPGRH+91eT+uPpfXmhs/qLtr8+5+FT3K8n81rMfXKq/TVzD3vBdpO9Q02xQcyl6jOHgf5UdwN9HVL9tYeZ5uNG/c73dAZvaysmmpRQkO+jQFnmmfZGL5ag73P+Glu1HRAKEDAA==",
      packageName: nodeApps.semver.packageName,
      version: nodeApps.semver.version,
    },
  },
  task: {
    binary: {
      binaries: githubApps.binaries.task.binaries as unknown as Record<string, AppStateGithub>,
      version: githubApps.apps.task.tag,
    },
  },
};
