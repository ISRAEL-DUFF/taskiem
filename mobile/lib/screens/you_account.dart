// You's settings for the person's own account (docs/spec/taskiem-for-
// everyone.md §7, §14; "As built: stage 3, Orders and account review
// fixes"): their photo (only they see it: businesses never do), which
// notifications businesses may send them, the privacy page (what
// businesses see, which have their number, how to ask one to erase it),
// and deleting the account with a fresh proof.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/notify.dart' show PhoneNotifications;
import '../app/passkey.dart' show PasskeyCancelled, passkeys;
import '../app/phone.dart' show showPhone;
import '../chat/approve.dart' show TkTextBox;
import '../chat/composer.dart' show Pickers;
import '../chat/my_chat.dart';
import '../kit/api_image.dart';
import '../kit/forms.dart' show TkSwitchRow;
import '../kit/kit.dart';
import '../kit/list.dart' show TkEmpty, TkLoading;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart';
import '../kit/sheet.dart' show TkCta, TkKitField, TkLines;
import 'sign_in.dart' show CodeBoxes;
import '../theme/look.dart';
import '../theme/theme.dart';
import 'second_proof.dart';
import 'settings_more.dart' show SettingsCard, SettingsNote, SettingsPage;
import 'signed_in.dart' show NotificationsCard;

// --- the photo ---

/// The person's photo's address (`user.photo.url`), or null.
String? myPhotoUrl(Me? me) {
  final p = me?.user?.photo;
  return p is Map && p['url'] is String ? p['url'] as String : null;
}

/// The person's photo, or their letters on a face.
class MyFace extends StatelessWidget {
  const MyFace({super.key, required this.auth, this.size = 72});
  final Auth auth;
  final double size;

  @override
  Widget build(BuildContext context) {
    final url = myPhotoUrl(auth.me);
    final name = auth.me?.user?.name ?? '';
    return ExcludeSemantics(
      child: SizedBox(
        width: size,
        height: size,
        child: url != null
            ? ClipOval(child: ApiImage(transport: auth.transport, path: url, width: size, height: size))
            : TkFace(tkInitials(name.isEmpty ? '?' : name), size: size, colors: tkFaceFor(name)),
      ),
    );
  }
}

/// The photo on You's settings: change it, or take it away. Only the
/// person sees it.
class MyPhotoCard extends StatefulWidget {
  const MyPhotoCard({super.key, required this.auth, this.pickers = const Pickers(), this.name, this.lines = const []});
  final Auth auth;
  final Pickers pickers;

  /// As the top of Settings (main's YouCard): the name beside the face,
  /// and lines under it (the phone, the email).
  final String? name;
  final List<String> lines;

  @override
  State<MyPhotoCard> createState() => _MyPhotoCardState();
}

class _MyPhotoCardState extends State<MyPhotoCard> {
  bool _busy = false;

  Future<void> _run(Future<void> Function() f, String done) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await f();
      await widget.auth.refreshMe();
      if (mounted) showTkToast(context, done);
    } on ApiError catch (e) {
      if (mounted) showTkToast(context, e.status == 413 ? 'That photo is too large. Try a smaller one.' : e.message, error: true);
    } on Object catch (e) {
      if (mounted) showTkToast(context, sayFailure(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _change() async {
    final PickedPhoto f;
    try {
      final got = await widget.pickers.photo(side: 1600);
      if (got == null || !mounted) return;
      f = (bytes: got.bytes, type: got.type, name: got.name);
    } on Object {
      if (mounted) showTkToast(context, 'Taskiem could not open your photos. Allow it in your phone’s settings, then try again.', error: true);
      return;
    }
    await _run(
      () => widget.auth.transport.upload('/v1/me/photo', method: 'PUT', bytes: f.bytes, filename: f.name.isEmpty ? 'photo.jpg' : f.name, type: f.type),
      'Your photo is saved',
    );
  }

  Future<void> _remove() async {
    final yes = await showTkConfirm(context, title: 'Remove your photo?', ok: 'Remove', danger: true);
    if (yes && mounted) await _run(widget.auth.api.deleteMyPhoto, 'Your photo is removed');
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final has = myPhotoUrl(widget.auth.me) != null;
    return Row(
      children: [
        MyFace(auth: widget.auth, size: 64),
        const SizedBox(width: 14),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (widget.name != null) ...[
                Text(widget.name!, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkDisplay(size: 19, letterSpacing: -0.3, color: t.text)),
                for (final l in widget.lines) Text(l, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 13, color: t.muted)),
                const SizedBox(height: 8),
              ],
              Wrap(
                spacing: 8,
                runSpacing: 6,
                children: [
                  TkSmallButton(label: _busy ? 'Saving…' : (has ? 'Change photo' : 'Add a photo'), onPressed: _busy ? null : _change),
                  if (has) TkSmallButton(label: 'Remove', onPressed: _busy ? null : _remove),
                ],
              ),
              const SizedBox(height: 6),
              Text('Only you see your photo. Businesses never do.', style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted)),
            ],
          ),
        ),
      ],
    );
  }
}

typedef PickedPhoto = ({List<int> bytes, String type, String name});

// --- notifications ---

/// Which notifications businesses may send the person (`GET`/`PUT
/// /v1/me/notifications`, for every phone they use), with this phone's own
/// switch above them.
class YouNotificationsPage extends StatefulWidget {
  const YouNotificationsPage({super.key, required this.auth, this.notify});
  final Auth auth;
  final PhoneNotifications? notify;

  @override
  State<YouNotificationsPage> createState() => _YouNotificationsPageState();
}

class _YouNotificationsPageState extends State<YouNotificationsPage> {
  YouNotifications? _n;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final n = await widget.auth.api.myNotifications();
      if (mounted) setState(() => (_n = n, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  static YouNotifications _with(YouNotifications n, {bool? messages, bool? updates, bool? offers, bool? people, bool? requests, bool? groups}) => YouNotifications(
    messages: messages ?? n.messages,
    updates: updates ?? n.updates,
    offers: offers ?? n.offers,
    groups: groups ?? n.groups,
    people: people ?? n.people,
    requests: requests ?? n.requests,
  );

  /// One kind on or off: shown at once, put back if refused.
  Future<void> _set(PutMyNotificationsRequest body, YouNotifications shown) async {
    final was = _n;
    setState(() => (_n = shown, _busy = true));
    try {
      final n = await widget.auth.api.putMyNotifications(body: body);
      if (mounted) setState(() => _n = n);
    } on Object catch (e) {
      if (!mounted) return;
      setState(() => _n = was);
      showTkToast(context, sayFailure(e), error: true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final n = _n;
    final notify = widget.notify;
    return SettingsPage(
      title: 'Notifications',
      children: [
        if (notify != null)
          ListenableBuilder(
            listenable: notify,
            builder: (context, _) {
              if (notify.available != true && !notify.noGoogle) return const SizedBox.shrink();
              return SettingsCard(
                title: 'This phone',
                children: [
                  if (notify.noGoogle)
                    NotificationsCard(notify: notify)
                  else ...[
                    TkSwitchRow(
                      label: 'Notify me on this phone',
                      hint: notify.isOn ? 'Businesses’ news comes to this phone as you choose below.' : 'This phone gets none until you turn this on.',
                      on: notify.isOn,
                      onChanged: notify.busy ? null : (v) => v ? notify.turnOn() : notify.turnOff(),
                    ),
                    TkListRow(
                      title: 'What this phone tells you',
                      icon: 'b-bell',
                      square: TkLook.of(context).orange,
                      onTap: () => tkPush<void>(
                        context,
                        SettingsPage(title: 'Notifications on this phone', backLabel: 'Back to Notifications', children: [NotificationsCard(notify: notify)]),
                      ),
                    ),
                  ],
                ],
              );
            },
          ),
        if (_error != null) TkErrorBox(_error!),
        if (n == null && _error == null) const TkLoading(label: 'Loading your notifications…'),
        if (n != null)
          SettingsCard(
            title: 'What businesses may tell you',
            children: [
              TkSwitchRow(
                label: 'Messages',
                hint: 'When someone at a business writes to you',
                on: n.messages,
                onChanged: _busy ? null : (v) => _set(PutMyNotificationsRequest(messages: v), _with(n, messages: v)),
              ),
              TkSwitchRow(
                label: 'Updates',
                hint: 'News of your orders and bookings',
                on: n.updates,
                onChanged: _busy ? null : (v) => _set(PutMyNotificationsRequest(updates: v), _with(n, updates: v)),
              ),
              TkSwitchRow(
                label: 'Offers',
                hint: 'From businesses you said yes to',
                on: n.offers,
                onChanged: _busy ? null : (v) => _set(PutMyNotificationsRequest(offers: v), _with(n, offers: v)),
              ),
            ],
          ),
        // People chats (people-chatting §13): a person's message, and a
        // stranger's first message waiting in Requests.
        if (n != null)
          SettingsCard(
            title: 'Chats with people',
            children: [
              TkSwitchRow(
                label: 'Messages from people',
                hint: 'When someone you chat with writes to you',
                on: n.people,
                onChanged: _busy ? null : (v) => _set(PutMyNotificationsRequest(people: v), _with(n, people: v)),
              ),
              TkSwitchRow(
                label: 'Message requests',
                hint: 'When someone new writes to you first',
                on: n.requests,
                onChanged: _busy ? null : (v) => _set(PutMyNotificationsRequest(requests: v), _with(n, requests: v)),
              ),
              // Groups (people-chatting §9, §13): their messages; a mention
              // still comes in a muted group unless turned off there (A7).
              // Invites to groups come with message requests.
              TkSwitchRow(
                label: 'Groups',
                hint: 'New messages in your groups. Invites come with message requests',
                on: n.groups,
                onChanged: _busy ? null : (v) => _set(PutMyNotificationsRequest(groups: v), _with(n, groups: v)),
              ),
            ],
          ),
        if (n != null) const SettingsNote('These are for every phone you use Taskiem on. What is turned off still waits for you in the chat.'),
      ],
    );
  }
}

// --- privacy ---

/// The words a request to erase starts with.
const eraseDraft = 'Hello, please erase the details you keep about me: ';

/// What businesses see of the person, which have their number, and how to
/// ask one to erase it (`GET /v1/me/privacy`).
class YouPrivacyPage extends StatefulWidget {
  const YouPrivacyPage({super.key, required this.auth, required this.onOpenChat, this.now = DateTime.now});
  final Auth auth;
  final OpenMyChat onOpenChat;
  final DateTime Function() now;

  @override
  State<YouPrivacyPage> createState() => _YouPrivacyPageState();
}

class _YouPrivacyPageState extends State<YouPrivacyPage> {
  MyPrivacyResponse? _p;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final p = await widget.auth.api.myPrivacy();
      if (mounted) setState(() => (_p = p, _error = null));
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    }
  }

  String _line(MyPrivacyResponseBusinessesItem b) {
    final last = b.lastOrderAt is String ? DateTime.tryParse(b.lastOrderAt as String) : null;
    return [
      if (b.orders > 0) b.orders == 1 ? '1 order' : '${b.orders} orders',
      if (last != null) 'last ${dayLabelOf(last, widget.now())}',
      if (b.orders == 0 && b.chatId is String) 'You chat with them',
    ].join(' · ');
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final p = _p;
    final phone = p?.shownToBusinesses.phone;
    return SettingsPage(
      title: 'Privacy',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (p == null && _error == null) const TkLoading(label: 'Loading…'),
        if (p != null) ...[
          SettingsCard(
            title: 'What businesses see',
            children: [
              // The app's label and value lines.
              TkLines([
                ('Your name', p.shownToBusinesses.name.isEmpty ? 'No name yet' : p.shownToBusinesses.name),
                ('Your phone', phone is String && phone.isNotEmpty ? showPhone(phone) : 'No phone on your account'),
                ('Your photo', 'Never. Only you see it.'),
              ]),
              Text('A business sees these when you chat with it or order from it.', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
            ],
          ),
          TkSection(
            title: 'Businesses that have your number',
            inset: 68,
            children: [
              if (p.businesses.isEmpty)
                const TkEmpty(icon: 'b-lock', title: 'None yet', line: 'A business has your number once you chat with it or order from it.')
              else
                for (final b in p.businesses)
                  TkListRow(
                    key: ValueKey('privacy ${b.businessId}'),
                    title: b.business,
                    sub: _line(b),
                    trailing: TkSmallButton(
                      label: 'Ask to erase',
                      semantics: 'Ask ${b.business} to erase your details',
                      onPressed: () => chatWithBusiness(
                        context,
                        widget.auth,
                        widget.onOpenChat,
                        business: b.businessId,
                        chat: b.chatId is String ? b.chatId as String : null,
                        from: 'order',
                        draft: eraseDraft,
                      ),
                    ),
                  ),
            ],
          ),
          SettingsCard(
            title: 'Having your details erased',
            children: [
              Text(p.erase.words, style: tkText(size: 14, lineHeight: 1.45, color: t.text)),
              Row(
                children: [
                  TkSquare('b-chat', color: look.teal, size: 26),
                  const SizedBox(width: 10),
                  Expanded(child: Text('Ask to erase opens your chat with that business, the request written for you.', style: tkText(size: 13, lineHeight: 1.4, color: t.muted))),
                ],
              ),
            ],
          ),
        ],
      ],
    );
  }
}

/// The day and month ("15 Nov"), with the year when it is not this one.
String dayLabelOf(DateTime d, DateTime now) {
  final l = d.toLocal(), n = now.toLocal();
  const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  return '${l.day} ${months[l.month - 1]}${l.year == n.year ? '' : ' ${l.year}'}';
}

// --- deleting the account ---

/// A refusal to delete, in a person's words (the server's sentence, the
/// businesses named).
String deleteProblem(Object e) {
  if (e is ApiError) {
    final names = (e.body['businesses'] as List?)?.whereType<String>().toList() ?? const <String>[];
    final list = names.isEmpty ? '' : names.length == 1 ? names.first : '${names.sublist(0, names.length - 1).join(', ')} and ${names.last}';
    if (e.has('owns_business')) {
      return 'You own ${list.isEmpty ? 'a business' : list} on Taskiem. Close it, or make someone else its owner and ask them to remove you, before you delete your account.';
    }
    if (e.has('in_business')) {
      return 'You are part of ${list.isEmpty ? 'a business' : list} on Taskiem. Ask its owner to remove you, then delete your account.';
    }
    if (e.status == 429) return 'Too many tries. Wait a moment and try again.';
  }
  return sayFailure(e);
}

enum _Proof { code, password }

/// Delete your account (§14): what goes and what the businesses keep, a
/// fresh proof (a code to the phone, the passkey, or the password), then
/// `POST /v1/me/delete`; on success the phone forgets everything of theirs.
class DeleteAccountScreen extends StatefulWidget {
  const DeleteAccountScreen({super.key, required this.auth});
  final Auth auth;

  @override
  State<DeleteAccountScreen> createState() => _DeleteAccountScreenState();
}

class _DeleteAccountScreenState extends State<DeleteAccountScreen> {
  _Proof? _way;
  final _code = TextEditingController();
  final _password = TextEditingController();
  String? _sentVia;
  bool _busy = false;
  String? _error;

  /// Why it cannot be deleted yet (a business owned or joined).
  String? _blocked;

  @override
  void dispose() {
    _code.dispose();
    _password.dispose();
    super.dispose();
  }

  /// The phone a code goes to, as it was when the page opened (once deleted,
  /// the account is gone from the phone before the page is).
  late final String? _phone = widget.auth.me?.user?.phone is String ? widget.auth.me!.user!.phone as String : null;
  bool get _hasPhone => _phone != null;

  Future<void> _run(Future<void> Function() f) async {
    if (_busy) return;
    setState(() => (_busy = true, _error = null));
    try {
      await f();
    } on PasskeyCancelled {
      // Put away: nothing to say.
    } on ApiError catch (e) {
      if (!mounted) return;
      if (e.has('owns_business') || e.has('in_business')) {
        setState(() => _blocked = deleteProblem(e));
      } else {
        setState(() => _error = deleteProblem(e));
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = sayFailure(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _sendCode({bool sms = false}) => _run(() async {
    final r = await widget.auth.api.deleteAccountStart(body: DeleteAccountStartRequest(via: sms ? 'sms' : null));
    _code.clear();
    setState(() => (_way = _Proof.code, _sentVia = r.sentVia));
  });

  /// The deletion itself, with [proof]; a phone that must first prove it is
  /// the person's (a second proof) does that, then it goes on.
  Future<void> _delete(DeleteAccountRequest proof) async {
    final yes = await showTkConfirm(
      context,
      title: 'Delete your account?',
      body: 'This cannot be undone.',
      ok: 'Delete my account',
      danger: true,
    );
    if (!yes || !mounted) return;
    await _run(() async {
      try {
        await widget.auth.api.deleteAccount(body: proof);
      } on ApiError catch (e) {
        if (!e.has('second_proof_required') || !mounted) rethrow;
        await showSecondProof(context, widget.auth, ways: ProofWays.of(e.body['ways']), onProved: () async {
          await widget.auth.api.deleteAccount(body: proof);
          await _gone();
        });
        return;
      }
      await _gone();
    });
  }

  /// Deleted: signed out here, and nothing of theirs left on the phone
  /// (its chats, its notifications' keys and shown notifications).
  Future<void> _gone() async {
    if (mounted) showTkToast(context, 'Your account is deleted.');
    await widget.auth.forgetDeleted();
  }

  void _withCode() {
    final code = _code.text.trim();
    if (code.length != 6) return setState(() => _error = 'Type the 6 numbers of the code.');
    unawaited(_delete(DeleteAccountRequest(confirm: true, code: code)));
  }

  void _withPassword() {
    final pw = _password.text;
    if (pw.isEmpty) return setState(() => _error = 'Type your password.');
    unawaited(_delete(DeleteAccountRequest(confirm: true, password: pw)));
  }

  void _withPasskey() => _run(() async {
    final options = await widget.auth.api.deleteAccountPasskeyOptions();
    final credential = await passkeys.get(options.publicKey);
    if (!mounted) return;
    setState(() => _busy = false);
    await _delete(DeleteAccountRequest(confirm: true, credential: PasskeyCredential.fromJson(credential)));
  });

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final phone = _phone ?? '';
    return SettingsPage(
      title: 'Delete your account',
      children: [
        SettingsCard(
          title: 'What happens',
          children: [
            for (final line in const [
              'Your chats, saved addresses, photo and notification choices are deleted, and you are signed out everywhere.',
              'Each business you chat with is told “This customer left Taskiem.”',
              'Businesses keep their own records of your orders and chats, as the law asks of them. To have those erased, ask each one first (Privacy).',
              'This cannot be undone.',
            ])
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Padding(padding: const EdgeInsets.only(top: 7, right: 10), child: Container(width: 5, height: 5, decoration: BoxDecoration(color: t.muted, shape: BoxShape.circle))),
                  Expanded(child: Text(line, style: tkText(size: 14, lineHeight: 1.45, color: t.text))),
                ],
              ),
          ],
        ),
        if (_blocked != null)
          Semantics(
            liveRegion: true,
            child: TkCallout(leading: TkSquare('b-alert', color: look.orange), title: 'Not yet', sub: _blocked!, subLines: 8, color: look.orange),
          )
        else ...[
          SettingsCard(
            title: 'Confirm it is you',
            children: [
              // The ways to confirm as the app's rows; the code as six
              // boxes; deleting is red.
              if (_way == null)
                TkSection(
                  inset: 56,
                  children: [
                    if (_hasPhone)
                      TkListRow(title: _busy ? 'Sending…' : 'Send a code to ${showPhone(phone)}', icon: 'b-phone', square: look.teal, onTap: _busy ? null : _sendCode),
                    TkListRow(title: 'Use my passkey', icon: 'b-key', square: look.lime, onTap: _busy ? null : _withPasskey),
                    TkListRow(title: 'Use my password', icon: 'b-lock', square: look.blue, onTap: _busy ? null : () => setState(() => _way = _Proof.password)),
                  ],
                ),
              if (_way == _Proof.code) ...[
                Semantics(
                  liveRegion: true,
                  child: Text(
                    'We sent a code to ${showPhone(phone)}${_sentVia == 'sms' ? ' by SMS' : (_sentVia == null ? '' : ' on ${_sentVia == 'telegram' ? 'Telegram' : 'WhatsApp'}')}.',
                    style: tkText(size: 14, lineHeight: 1.45, color: t.muted),
                  ),
                ),
                TkKitField(label: 'Code', field: false, child: CodeBoxes(key: const ValueKey('delete-code'), controller: _code)),
                if (_sentVia != 'sms') Center(child: TkLink(bold: true, label: 'Send by SMS instead', onTap: _busy ? null : () => _sendCode(sms: true))),
                TkCta(label: _busy ? 'Deleting…' : 'Delete my account', danger: true, onTap: _busy ? null : _withCode),
              ],
              if (_way == _Proof.password) ...[
                TkKitField(label: 'Your password', child: TkTextBox(key: const ValueKey('delete-password'), controller: _password, secret: true, size: 15, height: 46)),
                TkCta(label: _busy ? 'Deleting…' : 'Delete my account', danger: true, onTap: _busy ? null : _withPassword),
              ],
              if (_way != null) Center(child: TkLink(bold: true, label: 'Another way', onTap: _busy ? null : () => setState(() => (_way = null, _error = null)))),
            ],
          ),
          if (_error != null) Semantics(liveRegion: true, child: TkErrorBox(_error!)),
        ],
      ],
    );
  }
}
