// Signing up in the app as on WhatsApp (docs/spec/mobile-app-m4.md N7):
// the number proven with a code sent on WhatsApp, then the WhatsApp
// sign-up's questions in a chat with Taskiem (POST /v1/signup/app/...). At
// the end the business is made and this phone is signed in.

import 'package:flutter/foundation.dart';

import '../api/transport.dart';
import 'auth.dart';

enum AppSignupStep { phone, code, chat, ended, signIn }

/// A message of the conversation: Taskiem's, with its buttons, or the person's.
class SignupLine {
  const SignupLine({required this.text, this.mine = false, this.buttons = const []});
  final String text;
  final bool mine;
  final List<({String id, String title})> buttons;
}

class AppSignup extends ChangeNotifier {
  AppSignup(this.auth);
  final Auth auth;

  AppSignupStep step = AppSignupStep.phone;
  String phone = '';
  String? sent;
  final lines = <SignupLine>[];
  bool busy = false;
  String? error;
  String? _token;

  /// The buttons of Taskiem's last message, while it waits for an answer.
  List<({String id, String title})> get buttons =>
      step == AppSignupStep.chat && lines.isNotEmpty && !lines.last.mine ? lines.last.buttons : const [];

  bool _gone = false;

  @override
  void notifyListeners() {
    // The screen goes as the new business opens, while a turn may still end.
    if (!_gone) super.notifyListeners();
  }

  @override
  void dispose() {
    _gone = true;
    super.dispose();
  }

  Future<void> _run(Future<void> Function() f) async {
    busy = true;
    error = null;
    notifyListeners();
    final kept = lines.length;
    try {
      await f();
    } on ApiError catch (e) {
      error = e.message;
      if (e.has('sign_in')) step = AppSignupStep.signIn;
      if (e.has('ended')) step = AppSignupStep.ended;
    } on Offline {
      error = 'You’re offline. Check your connection and try again.';
    } on Object {
      error = 'Something went wrong. Try again.';
    }
    // A turn that did not go through: the answer is taken back, so its
    // question's buttons are there again to try.
    if (error != null && step == AppSignupStep.chat && lines.length > kept) {
      lines.removeRange(kept, lines.length);
    }
    busy = false;
    notifyListeners();
  }

  void _said(Object? messages) {
    for (final m in (messages as List? ?? const [])) {
      final j = (m as Map).cast<String, dynamic>();
      lines.add(SignupLine(
        text: j['text'] as String? ?? '',
        buttons: [
          for (final b in (j['buttons'] as List? ?? const []))
            (id: (b as Map)['id'] as String, title: b['title'] as String),
        ],
      ));
    }
  }

  /// A code to [number] on WhatsApp.
  Future<void> sendCode(String number) => _run(() async {
    phone = number.trim();
    final r = await auth.transport.send('POST', '/v1/signup/app/code', body: {'phone': phone});
    sent = (r as Map)['status'] as String?;
    step = AppSignupStep.code;
  });

  /// The code: the conversation begins with its first question.
  Future<void> verify(String code) => _run(() async {
    final r = (await auth.transport.send('POST', '/v1/signup/app/verify', body: {'phone': phone, 'code': code.trim()}) as Map);
    _token = r['token'] as String?;
    lines.clear();
    _said(r['messages']);
    step = AppSignupStep.chat;
  });

  /// An answer, typed or a button's (its [reply] id, shown as its [text]).
  Future<void> answer(String text, {String? reply}) => _run(() async {
    final words = text.trim();
    if (words.isEmpty && reply == null) return;
    lines.add(SignupLine(text: words, mine: true));
    notifyListeners();
    final r = (await auth.transport.send('POST', '/v1/signup/app/turn', body: {
      'phone': phone,
      'token': _token,
      if (reply == null) 'text': words else 'reply': reply,
      'bearer': true,
      'device': (await auth.store.device()).toJson(),
    }) as Map);
    _said(r['messages']);
    if (r['done'] == true) {
      final session = r['token'] as String?;
      if (session != null) {
        // Signed in: the app's home takes over, Taskiem's conversation first.
        await auth.signedInWith(session);
      } else {
        error = 'Your business is ready. Sign in with WhatsApp to open it.';
        step = AppSignupStep.signIn;
      }
    } else if (r['ended'] == true) {
      step = AppSignupStep.ended;
    }
  });

  /// From the beginning again.
  void restart() {
    step = AppSignupStep.phone;
    lines.clear();
    _token = null;
    error = null;
    notifyListeners();
  }
}
