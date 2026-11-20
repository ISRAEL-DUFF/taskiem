// Saved accounts (docs/spec/saved-objects.md; build plan S0; the web's
// pages/Saved.tsx): bank accounts a person or a business saves once and
// calls up by @name. A business's are on its Settings › Saved (everyone
// sees them, the number masked; those who manage them add, change and
// remove); a person's own are on You › Settings, private to them. Showing a
// number in full, and changing the account itself, ask for a passkey for
// that one account. A business with no payments account is offered the
// step to connect one, so the bank's name for each is checked: a Taskiem
// wallet here (an email and the director's BVN), or an account it already
// has, on the web's Connections.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/links.dart' show openInBrowser;
import '../app/passkey.dart' show PasskeyCancelled, passkeys;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart';
import '../screens/settings_more.dart' show SettingsNote, SettingsPage;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'cards.dart' show CardPill;
import 'groups.dart' show pickAction, pickOneOf, askSure;
import 'my_chat.dart' show sayFailure;
import 'words.dart' show CardTone;

/// A name as typed, as the server keeps it: no @, lowercase, spaces as "-".
String cleanSavedName(String typed) => typed.trim().toLowerCase().replaceFirst(RegExp('^@'), '').replaceAll(RegExp(r'[\s_]+'), '-');

/// What is wrong with a typed name, or null (lib/saved.ts nameProblem).
String? savedNameProblem(String typed) {
  final n = cleanSavedName(typed);
  if (n.isEmpty) return 'Give it a short name to call it up by, like @mum or @bayo.';
  if (n.length < 2 || n.length > 32) return 'A name is 2 to 32 letters or numbers, like @mum or @bola-stores.';
  if (!RegExp(r'^[a-z0-9][a-z0-9-]*[a-z0-9]$').hasMatch(n) || n.contains('--')) return 'A name uses only letters, numbers and "-" between them, like @bola-stores.';
  return null;
}

/// An account number as typed, without spaces, dots or dashes.
String cleanAccountNumber(String typed) => typed.replaceAll(RegExp(r'[\s.-]'), '');

/// Whether a saved account's name was checked with the bank, and the words.
({bool checked, String words}) savedCheck(SavedObject x) {
  final name = x.check.accountName;
  if (x.check.state == 'checked' && name is String && name.isNotEmpty) return (checked: true, words: 'Bank’s name: $name');
  return (checked: false, words: 'Name not checked yet');
}

/// A saved account's bank as its hint shows it ("GTBank" of "GTBank ••6789").
String savedShortHint(SavedObject x) {
  final i = x.hint.lastIndexOf(' ••');
  return i < 0 ? x.details.bankName : x.hint.substring(0, i);
}

/// One bank in a list: its code in that list ("" when chosen by name).
typedef SavedBank = ({String code, String name});

/// The banks to choose from, and the list they came from.
typedef SavedBanks = ({List<SavedBank> banks, String from});

/// The bank of a saved account, found again in a list: by its code when it
/// came from this list, else by name ("Bank", "PLC" and case set aside).
SavedBank? savedBankIn(SavedBanks list, SavedObjectDetails d) {
  if ((d.bankCode ?? '').isNotEmpty && d.codeFrom == list.from) {
    for (final b in list.banks) {
      if (b.code == d.bankCode) return b;
    }
  }
  String key(String s) => s.toLowerCase().split(RegExp('[^a-z0-9]+')).where((w) => w.isNotEmpty && !const {'bank', 'plc', 'limited', 'ltd'}.contains(w)).join(' ');
  for (final b in list.banks) {
    if (key(b.name) == key(d.bankName)) return b;
  }
  return null;
}

/// The person's own saved things, or the business's: the same calls on
/// /v1/me/saved or /v1/saved.
class SavedApi {
  SavedApi(this.auth, this.mine);
  final Auth auth;
  final bool mine;

  TaskiemApi get _a => auth.api;

  Future<({List<SavedObject> saved, int limit, bool canCheck})> list() async {
    if (mine) {
      final r = await _a.listMySaved();
      return (saved: r.saved, limit: r.limit, canCheck: false);
    }
    final r = await _a.listSaved();
    return (saved: r.saved, limit: r.limit, canCheck: r.canCheck ?? false);
  }

  Future<SavedBanks> banks() async {
    if (mine) {
      final r = await _a.listMyBanks();
      return (banks: [for (final b in r.banks) (code: b.code, name: b.name)], from: r.from);
    }
    final r = await _a.listBanks();
    return (banks: [for (final b in r.banks) (code: b.code, name: b.name)], from: r.from);
  }

  Future<SavedObjectResult> add(SavedObjectIn body) => mine ? _a.addMySaved(body: body) : _a.addSaved(body: body);

  Future<SavedObjectResult> change(String id, SavedObjectIn body) => mine ? _a.changeMySaved(id: id, body: body) : _a.changeSaved(id: id, body: body);

  Future<void> remove(String id) => mine ? _a.removeMySaved(id: id) : _a.removeSaved(id: id);

  /// A passkey's confirmation of [operation] (saved.change, saved.reveal)
  /// on one account.
  Future<PasskeyCredential> proof(String operation, String id) async {
    final pk = mine
        ? (await _a.mySavedStepUpOptions(body: MySavedStepUpOptionsRequest(operation: operation, target: id))).publicKey
        : (await _a.savedStepUpOptions(body: SavedStepUpOptionsRequest(operation: operation, target: id))).publicKey;
    return PasskeyCredential.fromJson(await passkeys.get(pk));
  }

  Future<String> reveal(String id) async {
    final p = await proof('saved.reveal', id);
    return mine
        ? (await _a.revealMySaved(id: id, body: RevealMySavedRequest(passkey: p))).accountNumber
        : (await _a.revealSaved(id: id, body: RevealSavedRequest(passkey: p))).accountNumber;
  }
}

/// You › Settings › Saved accounts (mine), or a business's Settings ›
/// Saved.
class SavedAccountsPage extends StatefulWidget {
  const SavedAccountsPage({super.key, required this.auth, required this.mine});
  final Auth auth;
  final bool mine;

  @override
  State<SavedAccountsPage> createState() => _SavedAccountsPageState();
}

class _SavedAccountsPageState extends State<SavedAccountsPage> {
  late final _api = SavedApi(widget.auth, widget.mine);
  List<SavedObject>? _list;
  int _limit = 0;
  SavedProviderResponse? _provider;
  String? _error;
  final _shown = <String, String>{};
  bool _checked = false;

  bool get _manage => widget.mine || widget.auth.can('saved.manage');

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final l = await _api.list();
      final p = widget.mine ? null : await widget.auth.api.savedProvider();
      if (!mounted) return;
      setState(() => (_list = l.saved, _limit = l.limit, _provider = p, _error = null));
      // Accounts saved before a payments account was connected are checked
      // as soon as one is.
      if (l.canCheck && !_checked) {
        _checked = true;
        final r = await widget.auth.api.checkAllSaved();
        if (!mounted) return;
        if (r.checked > 0) showTkToast(context, r.checked == 1 ? 'An account’s name was checked with the bank' : '${r.checked} accounts’ names were checked with the bank');
        await _load();
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  Future<void> _edit([SavedObject? x]) async {
    final p = _provider;
    final problem = await showTkSheet<String>(
      context,
      (_) => SavedAccountSheet(api: _api, thing: x, checks: !widget.mine && p?.connected != null),
    );
    if (problem == null || !mounted) return;
    if (problem.isNotEmpty) showTkToast(context, problem);
    await _load();
  }

  Future<void> _row(SavedObject x) async {
    final c = savedCheck(x);
    final pick = await pickAction(
      context,
      title: x.title,
      note: '@${x.name} · ${x.hint} · ${c.words}',
      actions: [
        ('change', 'Change', 'b-note', false),
        if (!_shown.containsKey(x.id)) ('show', 'Show the number', 'b-eye', false),
        ('remove', 'Remove', 'b-x', true),
      ],
    );
    if (!mounted) return;
    switch (pick) {
      case 'change':
        await _edit(x);
      case 'show':
        await _reveal(x);
      case 'remove':
        await _remove(x);
    }
  }

  Future<void> _reveal(SavedObject x) async {
    try {
      final n = await _api.reveal(x.id);
      if (mounted) setState(() => _shown[x.id] = n);
    } on PasskeyCancelled {
      return;
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
  }

  Future<void> _remove(SavedObject x) async {
    final yes = await askSure(context, title: 'Remove @${x.name}?', body: 'Its name is free to use again.', ok: 'Remove', danger: true);
    if (!yes || !mounted) return;
    try {
      await _api.remove(x.id);
      if (!mounted) return;
      showTkToast(context, '@${x.name} removed');
      await _load();
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    }
  }

  void _openWeb(String path) {
    final u = Uri.parse('${widget.auth.transport.base}$path');
    unawaited(openOrSay(context, () => openInBrowser(u), 'That page could not be opened.'));
  }

  Future<void> _openWallet() async {
    final opened = await showTkSheet<bool>(context, (_) => OpenWalletSheet(auth: widget.auth));
    if (opened == true && mounted) {
      showTkToast(context, 'Your Taskiem wallet is open');
      await _load();
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final list = _list;
    final p = _provider;
    final full = list != null && list.length >= _limit;
    final connect = p != null && p.connected == null && p.owner;
    return SettingsPage(
      title: widget.mine ? 'Saved accounts' : 'Saved',
      children: [
        if (_error != null)
          TkCallout(
            leading: TkSquare('b-warn', color: look.red),
            title: 'The saved accounts did not load',
            sub: _error,
            color: look.red,
            button: 'Try again',
            onPressed: () => unawaited(_load()),
          ),
        if (connect) ...[
          TkCallout(
            leading: TkSquare('b-bank', color: look.orange),
            title: 'Check names with the bank',
            sub: 'Connect a payments account, and the bank’s name for each account you save is checked.',
            color: look.orange,
            subLines: 3,
          ),
          TkSection(
            title: 'Connect a payments account',
            inset: 56,
            children: [
              if ((p.wallet?.available ?? false) && (p.wallet?.plan ?? false))
                TkListRow(title: 'Open a Taskiem wallet', sub: 'Your email and the director’s BVN', icon: 'b-bank', square: look.lime, onTap: _openWallet),
              TkListRow(
                title: 'I already have an account',
                sub: [for (final x in p.providers ?? const <SavedProviderResponseProvidersItem>[]) x.name].join(', '),
                icon: 'b-tools',
                square: look.teal,
                onTap: () => _openWeb('/connections'),
              ),
            ],
          ),
        ],
        if (list == null && _error == null) const TkLoading(label: 'Loading the saved accounts…'),
        if (list != null && list.isEmpty)
          const TkPanel(children: [TkEmpty(icon: 'b-bank', title: 'No saved accounts yet', line: 'Save one once, and call it up by its name, like @bayo.')]),
        if (list != null && (list.isNotEmpty || (_manage && !full)))
          TkSection(
            title: list.isEmpty ? null : 'Accounts',
            inset: 56,
            children: [
              for (final x in list)
                () {
                  final c = savedCheck(x);
                  final shown = _shown[x.id];
                  return TkListRow(
                    key: ValueKey('saved ${x.id}'),
                    title: x.title,
                    // The bank and its number kept together on one line.
                    sub: '@${x.name} · ${(shown != null ? '${savedShortHint(x)} $shown' : x.hint).replaceAll(' ', '\u00a0')}',
                    icon: 'b-bank',
                    square: c.checked ? look.lime : look.orange,
                    trailing: CardPill(c.checked ? 'Checked' : 'Not checked', c.checked ? CardTone.lime : CardTone.warn),
                    semanticsLabel: '${x.title}, @${x.name}, ${x.hint}. ${c.words}',
                    onTap: x.toChange ? () => _row(x) : null,
                  );
                }(),
              if (_manage && !full) TkListRow(title: 'Save an account', icon: 'b-plus', square: look.teal, onTap: () => _edit()),
            ],
          ),
        if (p?.connected != null) SettingsNote('Names are checked with the bank through ${p!.connected!.wallet ? 'your Taskiem wallet' : p.connected!.name}.'),
        SettingsNote(
          widget.mine
              ? 'Only you see these. No business you work in sees this list.'
              : full
              ? 'You have $_limit saved accounts, the most you can keep.'
              : _manage
              ? 'Everyone in the business sees them, with the number masked.'
              : 'The business’s owners and admins add and change them.',
        ),
        if (_manage && list != null && list.isNotEmpty) Text('Changing an account, or seeing its number in full, asks for your passkey.', style: tkHint(t)),
      ],
    );
  }
}

/// Save an account, or change one (its title, name, note, or the account
/// itself, which asks for a passkey). With a payments account, the bank's
/// name for it shows before it is saved. Pops "" when saved, or the reason
/// its name could not be checked just now.
class SavedAccountSheet extends StatefulWidget {
  const SavedAccountSheet({super.key, required this.api, this.thing, required this.checks});
  final SavedApi api;
  final SavedObject? thing;
  final bool checks;

  @override
  State<SavedAccountSheet> createState() => _SavedAccountSheetState();
}

class _SavedAccountSheetState extends State<SavedAccountSheet> {
  late final SavedObject? _x = widget.thing;
  late final _name = TextEditingController(text: _x == null ? '' : '@${_x.name}');
  late final _title = TextEditingController(text: _x?.title ?? '');
  late final _number = TextEditingController();
  late final _note = TextEditingController(text: _x?.details.note ?? '');
  SavedBanks? _banks;
  SavedBank? _bank;
  SavedBank? _wasBank;
  String? _bankName;
  String _asked = '';
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _number.addListener(_numberChanged);
    unawaited(_loadBanks());
  }

  @override
  void dispose() {
    for (final c in [_name, _title, _number, _note]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _loadBanks() async {
    try {
      final b = await widget.api.banks();
      if (!mounted) return;
      final x = _x;
      setState(() {
        _banks = b;
        if (x != null) _bank = _wasBank = savedBankIn(b, x.details);
      });
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  bool get _accountChanges => _x == null || cleanAccountNumber(_number.text).isNotEmpty || (_bank != null && _bank != _wasBank);

  SavedObjectIn _bankFields({String? number}) {
    final b = _bank;
    return SavedObjectIn(
      bankName: b?.name,
      bankCode: b == null || b.code.isEmpty ? null : b.code,
      codeFrom: b == null || b.code.isEmpty ? null : _banks?.from,
      accountNumber: number,
    );
  }

  void _numberChanged() {
    setState(() {});
    _askName();
  }

  /// The bank's name, asked once a bank and 10 digits are there.
  Future<void> _askName() async {
    final n = cleanAccountNumber(_number.text);
    final b = _bank;
    if (!widget.checks || b == null || !RegExp(r'^\d{10}$').hasMatch(n)) {
      if (_bankName != null) setState(() => _bankName = null);
      return;
    }
    final key = '${b.code}/${b.name}/$n';
    if (key == _asked) return;
    _asked = key;
    try {
      final r = await widget.api.auth.api.nameCheck(body: _bankFields(number: n));
      if (mounted && _asked == key) setState(() => _bankName = r.accountName);
    } on ApiError catch (e) {
      if (mounted && _asked == key) setState(() => _bankName = e.status == 400 ? '' : null);
    } on Object {
      if (mounted && _asked == key) setState(() => _bankName = null);
    }
  }

  Future<void> _pickBank() async {
    final list = _banks;
    if (list == null) return;
    final v = await pickOneOf(
      context,
      title: 'Bank',
      value: _bank == null ? null : '${_bank!.code}|${_bank!.name}',
      choices: [for (final b in list.banks) ('${b.code}|${b.name}', b.name, '')],
    );
    if (v == null || !mounted) return;
    setState(() => _bank = list.banks.firstWhere((b) => '${b.code}|${b.name}' == v));
    unawaited(_askName());
  }

  Future<void> _save() async {
    final x = _x;
    final number = cleanAccountNumber(_number.text);
    final problem =
        savedNameProblem(_name.text) ??
        (_title.text.trim().isEmpty ? 'Give it a title, like Mum or Bola Stores.' : null) ??
        (x == null && _bank == null ? 'Choose the bank.' : null) ??
        ((x == null || number.isNotEmpty) && !RegExp(r'^\d{10}$').hasMatch(number) ? 'An account number is 10 digits.' : null);
    if (problem != null) return setState(() => _error = problem);
    setState(() => (_busy = true, _error = null));
    try {
      final account = _accountChanges ? _bankFields(number: number.isEmpty ? null : number) : const SavedObjectIn();
      final body = SavedObjectIn(
        name: cleanSavedName(_name.text),
        title: _title.text.trim(),
        note: _note.text.trim(),
        kind: x == null ? 'bank_account' : null,
        bankName: account.bankName,
        bankCode: account.bankCode,
        codeFrom: account.codeFrom,
        accountNumber: account.accountNumber,
        passkey: x != null && _accountChanges ? await widget.api.proof('saved.change', x.id) : null,
      );
      final r = x == null ? await widget.api.add(body) : await widget.api.change(x.id, body);
      if (mounted) Navigator.of(context).pop(r.checkProblem ?? '');
    } on PasskeyCancelled {
      if (mounted) setState(() => _busy = false);
    } on ApiError catch (e) {
      if (mounted) setState(() => (_busy = false, _error = e.has('name_taken') ? 'That name is taken: choose another.' : e.message));
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final x = _x;
    final b = _bank;
    return TkFormSheet(
      title: x == null ? 'Save an account' : 'Change @${x.name}',
      crumb: 'Saved',
      action: _busy ? 'Saving…' : (x == null ? 'Save account' : 'Save changes'),
      onAction: _busy ? null : _save,
      children: [
        TkFormGroup(
          children: [
            TkKitField(label: 'Name to call it up by', hint: 'Such as @mum or @bayo', child: TkTextBox(controller: _name, max: 33, plain: true)),
            TkKitField(label: 'Title', hint: 'How it is shown, such as Bayo (rice)', child: TkTextBox(controller: _title, max: 80)),
          ],
        ),
        TkFormGroup(
          children: [
            TkKitField(
              label: 'Bank',
              field: false,
              child: TkPickBox(label: 'Bank', shown: _banks == null ? 'Loading the banks…' : (b?.name ?? 'Choose the bank'), onTap: _pickBank),
            ),
            TkKitField(
              label: 'Account number',
              hint: x == null ? '10 digits' : 'Leave it empty to keep ${x.details.accountNumber ?? 'the number'}',
              child: TkTextBox(controller: _number, digits: 10),
            ),
            TkKitField(label: 'Note', hint: 'Optional, for your own reference', child: TkTextBox(controller: _note, max: 200)),
          ],
        ),
        if (_bankName != null && _bankName!.isNotEmpty)
          TkCallout(leading: TkSquare('b-check', color: look.lime), title: 'The bank’s name for it', sub: _bankName, color: look.lime),
        if (_bankName == '')
          TkCallout(
            leading: TkSquare('b-warn', color: look.red),
            title: 'The bank has no account with that number',
            sub: 'Check the number and the bank.',
            color: look.red,
          ),
        if (x != null && _accountChanges)
          TkCallout(leading: TkSquare('b-key', color: look.violet), title: 'Changing the account asks for your passkey', color: look.violet),
        if (_error != null) TkCallout(leading: TkSquare('b-warn', color: look.red), title: _error!, color: look.red),
      ],
    );
  }
}

/// Open a Taskiem wallet (POST /v1/settings/receive-money): the business's
/// email and the director's BVN, sent once to the partner bank; Taskiem
/// does not keep the BVN. Pops true when it is open.
class OpenWalletSheet extends StatefulWidget {
  const OpenWalletSheet({super.key, required this.auth});
  final Auth auth;

  @override
  State<OpenWalletSheet> createState() => _OpenWalletSheetState();
}

class _OpenWalletSheetState extends State<OpenWalletSheet> {
  final _email = TextEditingController();
  final _bvn = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _email.dispose();
    _bvn.dispose();
    super.dispose();
  }

  Future<void> _open() async {
    final email = _email.text.trim();
    final bvn = _bvn.text.replaceAll(RegExp(r'\D'), '');
    if (!email.contains('@') || bvn.length != 11) return setState(() => _error = 'Give your business email and the director’s 11-digit BVN.');
    setState(() => (_busy = true, _error = null));
    try {
      await widget.auth.transport.send('POST', '/v1/settings/receive-money', body: {'business_email': email, 'bvn': bvn});
      if (mounted) Navigator.of(context).pop(true);
    } on Object catch (e) {
      if (mounted) setState(() => (_busy = false, _error = sayFailure(e)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return TkFormSheet(
      title: 'Open a Taskiem wallet',
      crumb: 'Saved',
      action: _busy ? 'Opening…' : 'Open my wallet',
      onAction: _busy ? null : _open,
      children: [
        TkCallout(
          leading: TkSquare('b-bank', color: look.lime),
          title: 'An account number of your own',
          sub: 'Customers pay into it, and the bank’s name for each account you save is checked through it.',
          color: look.lime,
          subLines: 3,
        ),
        TkFormGroup(
          children: [
            TkKitField(label: 'Your business email', child: TkTextBox(controller: _email, plain: true)),
            TkKitField(
              label: 'The director’s BVN',
              hint: '11 digits, sent once to the partner bank to open the account. Taskiem does not keep it.',
              child: TkTextBox(controller: _bvn, digits: 11),
            ),
          ],
        ),
        if (_error != null) TkCallout(leading: TkSquare('b-warn', color: look.red), title: _error!, color: look.red),
      ],
    );
  }
}
