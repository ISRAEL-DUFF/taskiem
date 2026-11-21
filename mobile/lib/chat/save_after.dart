// Saving after an action (saved objects S3, docs/spec/saved-objects.md
// §9.3: after a new customer's first order, after a delivery to a new
// address): offered once, quietly, saved with one tap; suggested, never
// done for them. A name already taken asks for another in a small sheet.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart' show ApiError;
import '../app/auth.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../theme/look.dart';
import 'approve.dart' show TkTextBox;
import 'my_chat.dart' show sayFailure;
import 'saved_accounts.dart' show SavedApi, cleanSavedName, savedNameProblem;
import 'saved_picks.dart' show SavedPicks;

/// A name made from a title, as the server makes one (engine/saved
/// NameFor): lowercase letters and digits, words joined by "-", at most 32;
/// '' when none fits ("Bola Ade" → bola-ade).
String savedNameFor(String title) {
  final b = StringBuffer();
  var dash = false;
  for (final r in title.toLowerCase().runes) {
    final c = String.fromCharCode(r);
    if (RegExp('[a-z0-9]').hasMatch(c)) {
      if (dash && b.isNotEmpty) b.write('-');
      b.write(c);
      dash = false;
    } else {
      dash = true;
    }
  }
  var n = b.toString();
  if (n.length > 32) n = n.substring(0, 32).replaceFirst(RegExp(r'-+$'), '');
  return savedNameProblem(n) == null ? n : '';
}

/// A typed address as a place's title: its first part ("12 Admiralty Way"
/// of "12 Admiralty Way, Lekki"), else "Delivery place".
String placeTitleFor(String address) {
  final first = address.split(RegExp(r'[,\n(]')).first.trim();
  if (first.isEmpty || savedNameFor(first).isEmpty) return 'Delivery place';
  return first.length > 60 ? first.substring(0, 60).trimRight() : first;
}

/// Saves with [name] ('' for the server's own), and when the name is taken
/// asks for another in a sheet and saves with it. Null when put away.
/// Other failures are thrown.
Future<SavedObject?> saveOrAskName(BuildContext context, {required String name, required Future<SavedObject> Function(String? name) save}) async {
  try {
    return await save(name.isEmpty ? null : name);
  } on ApiError catch (e) {
    if (!e.has('name_taken')) rethrow;
  }
  if (!context.mounted) return null;
  return showTkSheet<SavedObject>(context, (_) => SaveNameSheet(taken: name, save: save));
}

/// The business's saved things, POST /v1/saved: one place.
Future<SavedObject> savePlace(Auth auth, {required String name, required String title, required String address, String phone = ''}) async =>
    (await SavedApi(auth, false).add(SavedObjectIn(name: name, title: title, kind: 'place', details: {'line': address, if (phone.isNotEmpty) 'phone': phone})))
        .saved;

/// Something was saved: the forms' Saved buttons and the chats' pickers
/// read their lists again.
void savedNow() => SavedPicks.saves++;

/// The name is taken: another, typed ("-" between words), and saved with it.
class SaveNameSheet extends StatefulWidget {
  const SaveNameSheet({super.key, required this.taken, required this.save});

  /// The name that was taken ('' when the server made it).
  final String taken;
  final Future<SavedObject> Function(String? name) save;

  @override
  State<SaveNameSheet> createState() => _SaveNameSheetState();
}

class _SaveNameSheetState extends State<SaveNameSheet> {
  late final _name = TextEditingController(text: widget.taken.isEmpty ? '' : '@${widget.taken}');
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _name.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  Future<void> _go() async {
    final problem = savedNameProblem(_name.text);
    if (problem != null) return setState(() => _error = problem);
    setState(() => (_busy = true, _error = null));
    try {
      final x = await widget.save(cleanSavedName(_name.text));
      if (mounted) Navigator.of(context).pop(x);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_busy = false, _error = e.has('name_taken') ? 'That name is taken too: choose another.' : e.message));
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkFormSheet(
      title: 'Another name',
      crumb: widget.taken.isEmpty ? 'That name is taken' : '@${widget.taken} is taken',
      action: _busy ? 'Saving…' : 'Save',
      onAction: _busy || cleanSavedName(_name.text).isEmpty ? null : _go,
      second: 'Not now',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkFormGroup(
          children: [
            TkKitField(
              label: 'Name to call it up by',
              hint: 'Letters and numbers, with "-" between words',
              child: TkTextBox(controller: _name, max: 33, plain: true),
            ),
          ],
        ),
        if (_error != null)
          TkCallout(
            leading: TkSquare('b-warn', color: look.red),
            title: _error!,
            color: look.red,
          ),
      ],
    );
  }
}
