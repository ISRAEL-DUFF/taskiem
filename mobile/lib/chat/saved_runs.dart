// The runs that read one of the business's saved things (build plan H6,
// web SavedRuns.tsx, GET /v1/saved/{id}/runs): under a saved thing's
// sheet, newest first, for those who may read runs. A person's own saved
// things are never read by runs, so they have none.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/mobile.dart';
import '../theme/look.dart';
import 'words.dart' show CardTone;

/// Which steps read it, in words (savedUses.ts stepsWords).
String savedStepsWords(List<String> steps) {
  if (steps.isEmpty) return 'Read when the run started';
  if (steps.length == 1) return 'Read by step ${steps.first}';
  return 'Read by steps ${steps.sublist(0, steps.length - 1).join(', ')} and ${steps.last}';
}

/// The runs that read a business's saved thing [id]; nothing for those
/// who may not read runs, or while they are read.
class SavedRunsSection extends StatefulWidget {
  const SavedRunsSection({super.key, required this.auth, required this.id});
  final Auth auth;
  final String id;

  @override
  State<SavedRunsSection> createState() => _SavedRunsSectionState();
}

class _SavedRunsSectionState extends State<SavedRunsSection> {
  List<SavedUse>? _runs;

  @override
  void initState() {
    super.initState();
    if (widget.auth.can('run.read')) _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.listSavedRuns(id: widget.id);
      if (mounted) setState(() => _runs = r.runs);
    } on Object {
      // Not shown: the thing's own fields still are.
    }
  }

  @override
  Widget build(BuildContext context) {
    final runs = _runs;
    if (runs == null) return const SizedBox.shrink();
    final look = TkLook.of(context);
    return TkSection(
      title: 'Runs that read it',
      inset: 56,
      empty: runs.isEmpty ? 'No workflow has read it yet. A run reads it when it starts, so a change is used from the next run.' : null,
      children: [
        for (final u in runs)
          TkListRow(
            key: Key('saved-run-${u.runId}'),
            title: u.workflow.isEmpty ? 'A workflow' : u.workflow,
            sub: '${savedStepsWords(u.steps)} · version ${u.version}',
            icon: 'b-flow',
            square: u.status == 'failed' ? look.red : look.teal,
            status: switch (u.status) {
              'completed' => 'Done',
              'failed' => 'Failed',
              'running' || 'waiting' || 'queued' => 'Going',
              _ => null,
            },
            statusTone: u.status == 'failed' ? CardTone.danger : (u.status == 'completed' ? CardTone.lime : CardTone.plain),
            chevron: false,
          ),
      ],
    );
  }
}
