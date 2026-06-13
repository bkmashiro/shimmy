class EvaluationResponse:
    def __init__(self):
        self.is_correct = False
        self.latex = None
        self._feedback = []
        self._feedback_tags = {}
        self.latex = ""
        self.simplified = ""

    def get_feedback(self, tag):
        return self._feedback_tags.get(tag, None)

    def add_feedback(self, feedback_item):
        if isinstance(feedback_item, tuple):
            self._feedback.append(feedback_item[1])
            self._feedback_tags.update({feedback_item[0]: len(self._feedback)-1})
        self._feedback_tags

    def _serialise_feedback(self) -> str:
        return "<br>".join(x[1] if isinstance(x, tuple) else x for x in self._feedback)

    def serialise(self, include_test_data=False) -> dict:
        out = dict(is_correct=self.is_correct, feedback=self._serialise_feedback())
        if include_test_data is True:
            out.update(dict(tags=self._feedback_tags))
        if self.latex is not None:
            out.update(dict(response_latex=self.latex))
        if self.simplified is not None:
            out.update(dict(response_simplified=self.simplified))
        return out

    def __getitem__(self, key):
        return self.serialise(include_test_data=True)[key]
