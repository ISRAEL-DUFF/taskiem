// Saved addresses (docs/spec/taskiem-for-everyone.md §13; "As built: stage 3,
// Orders and addresses"): kept on the person's side (`/v1/me/addresses`),
// up to 20, the default first; each a name (Home, Work), the street and
// number, the area, a landmark and a phone for the rider when it is not
// the person's own. In You's Settings, and offered at their shop's check
// step. A business sees only the address copied onto its order.

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show TkSwitchRow;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../screens/settings_more.dart' show SettingsNote, SettingsPage;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'my_chat.dart' show sayFailure;

/// How many addresses a person may keep.
const maxSavedAddresses = 20;

/// The longest street and number a saved address keeps.
const maxSavedLine = 200;

/// A saved address in a line: "12 Herbert Macaulay Way, Yaba".
String addressLine(SavedAddress a) => [a.line, if (a.area.isNotEmpty) a.area].join(', ');

/// Settings > Saved addresses.
class SavedAddressesPage extends StatefulWidget {
  const SavedAddressesPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<SavedAddressesPage> createState() => _SavedAddressesPageState();
}

class _SavedAddressesPageState extends State<SavedAddressesPage> {
  List<SavedAddress>? _list;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.auth.api.myAddresses();
      if (mounted) setState(() => (_list = r.addresses, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _edit([SavedAddress? a]) async {
    final changed = await showTkSheet<bool>(context, (_) => AddressSheet(auth: widget.auth, address: a));
    if (changed == true) await _load();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final list = _list;
    final full = (list?.length ?? 0) >= maxSavedAddresses;
    return SettingsPage(
      title: 'Saved addresses',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (list == null && _error == null) const TkLoading(label: 'Loading your addresses…'),
        if (list != null && list.isEmpty)
          const TkPanel(children: [TkEmpty(icon: 'pin', title: 'No saved addresses yet', line: 'Save one here, or when you order, and pick it next time.')]),
        if (list != null)
          TkSection(
            inset: 56,
            children: [
              for (final a in list)
                TkListRow(
                  key: ValueKey('address ${a.id}'),
                  title: a.label,
                  sub: [addressLine(a), if (a.landmark.isNotEmpty) a.landmark].join(' · '),
                  icon: 'b-pin',
                  square: a.default_ ? look.lime : look.orange,
                  trailing: a.default_ ? Text('Default', style: tkText(size: 14, color: t.muted)) : null,
                  semanticsLabel: '${a.label}: ${addressLine(a)}${a.default_ ? ', your default' : ''}',
                  onTap: () => _edit(a),
                ),
              if (!full) TkListRow(title: 'Add an address', icon: 'b-plus', square: look.teal, onTap: () => _edit()),
            ],
          ),
        SettingsNote(
          full
              ? 'You can keep up to 20 addresses. Delete one you no longer use to add another.'
              : 'Only you see this list. A business sees only the address on your order with it.',
        ),
      ],
    );
  }
}

/// Add or change a saved address; Delete when changing. Pops true when
/// the list changed.
class AddressSheet extends StatefulWidget {
  const AddressSheet({super.key, required this.auth, this.address});
  final Auth auth;
  final SavedAddress? address;

  @override
  State<AddressSheet> createState() => _AddressSheetState();
}

class _AddressSheetState extends State<AddressSheet> {
  late final SavedAddress? _a = widget.address;
  late final _label = TextEditingController(text: _a?.label ?? '');
  late final _line = TextEditingController(text: _a?.line ?? '');
  late final _area = TextEditingController(text: _a?.area ?? '');
  late final _landmark = TextEditingController(text: _a?.landmark ?? '');
  late final _phone = TextEditingController(text: _a?.phone is String ? _a!.phone as String : '');
  late bool _default = _a?.default_ ?? false;
  bool _busy = false;
  String? _error;
  String? _phoneError;

  @override
  void dispose() {
    for (final c in [_label, _line, _area, _landmark, _phone]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _save() async {
    if (_label.text.trim().isEmpty || _line.text.trim().isEmpty) {
      return setState(() => _error = 'Give the address a name (Home, Work) and its street and number.');
    }
    setState(() => (_busy = true, _error = null, _phoneError = null));
    final a = _a;
    final body = SavedAddressIn(
      label: _label.text.trim(),
      line: _line.text.trim(),
      area: _area.text.trim(),
      landmark: _landmark.text.trim(),
      // Empty removes a phone it had; a new one sends none.
      phone: _phone.text.trim().isEmpty ? (a == null ? null : '') : _phone.text.trim(),
      default_: _default && !(a?.default_ ?? false) ? true : null,
    );
    try {
      if (a == null) {
        await widget.auth.api.addMyAddress(body: body);
      } else {
        await widget.auth.api.editMyAddress(id: a.id, body: body);
      }
      if (mounted) Navigator.of(context).pop(true);
    } on ApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        if (e.has('phone_invalid')) {
          _phoneError = e.message;
        } else {
          _error = e.message;
        }
      });
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  Future<void> _delete() async {
    final a = _a!;
    final yes = await showTkConfirm(context, title: 'Delete ${a.label}?', body: addressLine(a), ok: 'Delete', danger: true);
    if (!yes || !mounted) return;
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.api.deleteMyAddress(id: a.id);
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final a = _a;
    return TkFormSheet(
      title: a == null ? 'Add an address' : 'Change ${a.label}',
      crumb: 'Saved addresses',
      action: _busy ? 'Saving…' : 'Save',
      onAction: _busy ? null : _save,
      second: a == null ? null : 'Delete',
      secondDanger: true,
      onSecond: a == null || _busy ? null : _delete,
      children: [
        TkFormGroup(
          children: [
            TkKitField(label: 'Name', hint: 'Home, Work, Mum’s place', child: TkTextBox(controller: _label, max: 40)),
            TkKitField(label: 'Street and number', child: TkTextBox(controller: _line, lines: 2, max: 200)),
            TkKitField(label: 'Area', hint: 'Optional', child: TkTextBox(controller: _area, max: 80)),
            TkKitField(label: 'Landmark', hint: 'Optional: what the rider looks for', child: TkTextBox(controller: _landmark, max: 200)),
            TkKitField(
              label: 'Phone for delivery',
              hint: 'Optional: when the rider should call another number',
              error: _phoneError,
              child: TkTextBox(controller: _phone, phone: true),
            ),
          ],
        ),
        if (!(a?.default_ ?? false))
          TkFormGroup(children: [TkSwitchRow(label: 'Make this my default', on: _default, onChanged: (v) => setState(() => _default = v))]),
        if (_error != null) TkErrorBox(_error!),
      ],
    );
  }
}
