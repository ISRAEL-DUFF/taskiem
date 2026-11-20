// A form's Saved button (saved objects S2, docs/spec/saved-objects.md §6.4):
// the owner's saved things of the kind the form asks for (a person, a
// place), the business's and the person's own, one picked to fill it in.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/modal.dart' show showTkToast;
import 'groups.dart' show pickOneOf;
import 'my_chat.dart' show sayFailure;
import 'saved_accounts.dart' show savedKinds;

/// One of the saved things of [kind], the person's own first, then the
/// business's (none for a person with no business), each by its title with
/// its @name and hint; null when none is picked. Nothing of the kind, or a
/// list that cannot be read, is said in a toast.
Future<SavedObject?> pickSaved(BuildContext context, Auth auth, {required String kind, required String title}) async {
  final List<SavedObject> mine, business;
  try {
    mine = (await auth.api.listMySaved(kind: kind)).saved;
    business = auth.personal ? const [] : (await auth.api.listSaved(kind: kind)).saved;
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, sayFailure(e), error: true);
    return null;
  }
  final all = [for (final x in [...mine, ...business]) if (x.kind == kind) x];
  if (!context.mounted) return null;
  if (all.isEmpty) {
    showTkToast(context, 'Nothing saved of this kind yet');
    return null;
  }
  final both = mine.isNotEmpty && business.isNotEmpty;
  final key = await pickOneOf(
    context,
    title: title,
    searchHint: all.length > 6 ? 'Search by name' : null,
    icon: savedKinds[kind]?.icon ?? 'b-note',
    choices: [
      for (final x in all)
        (
          '${x.owner}:${x.id}',
          x.title.isEmpty ? '@${x.name}' : x.title,
          ['@${x.name}', x.hint, if (both) x.owner == 'mine' ? 'Yours' : 'The business’s'].where((w) => w.isNotEmpty).join(' · '),
        ),
    ],
  );
  return all.where((x) => '${x.owner}:${x.id}' == key).firstOrNull;
}

/// A saved place as an address line: "12 Admiralty Way, Lekki (by the
/// filling station)".
String placeLine(SavedObjectDetails d) {
  final line = [d.line ?? '', d.area ?? ''].map((w) => w.trim()).where((w) => w.isNotEmpty).join(', ');
  final mark = (d.landmark ?? '').trim();
  return mark.isEmpty ? line : '$line ($mark)';
}
