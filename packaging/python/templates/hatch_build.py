"""Custom build hook to set platform-specific wheel tags.

Each platform package contains a single native binary.
The wheel tag is read from [tool.hatch.build.hooks.custom] config in pyproject.toml.
"""

from hatchling.builders.hooks.plugin.interface import BuildHookInterface


class CustomBuildHook(BuildHookInterface):
    PLUGIN_NAME = "custom"

    def initialize(self, version, build_data):
        wheel_tag = self.config.get("wheel-tag", "")

        if not wheel_tag:
            raise ValueError(
                "[tool.hatch.build.hooks.custom] must define 'wheel-tag'. "
                "Example: wheel-tag = 'manylinux2014_x86_64'"
            )

        build_data["tag"] = f"py3-none-{wheel_tag}"
