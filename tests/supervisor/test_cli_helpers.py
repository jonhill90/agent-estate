class FakeMetadataTransport:
    """Stands in for `TmuxTransport.metadata`/`get_option` -- lane_free's only
    tmux touches (agent-dotfiles#216 added the option read). Shared by
    VerifyCallerCliTest and LaneFreeTest."""

    def __init__(self, metadata, options=None):
        self._metadata = metadata
        self._options = options or {}
        self.calls = []

    def metadata(self, target):
        self.calls.append(target)
        return self._metadata

    def get_option(self, target, name):
        self.calls.append((target, name))
        return self._options.get(name, "")
