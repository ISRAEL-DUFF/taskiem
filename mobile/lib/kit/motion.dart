// The web's motion (web/src/motion.css, the chat boards' moments in
// web/src/chat.css, styles.css's rise-in, pop and live) as the app's parts:
// each entrance takes the web's keyframes, times and curves, from the
// tokens (lib/theme/tokens.g.dart) where the web takes its variables. Motion
// turned down on the phone: nothing moves or fades; things are simply there.

import 'dart:math' as math;

import 'package:flutter/widgets.dart';

import '../theme/theme.dart';

/// Whether the phone asks for no motion.
bool tkStill(BuildContext context) => MediaQuery.maybeDisableAnimationsOf(context) ?? false;

/// Whether the motions that never end (a live dot's pulse) run. Off in
/// tests (test/flutter_test_config.dart), where a screen must settle.
bool tkRepeatingMotion = true;

/// The [index]th of a list's delay: [each] apart, from [start], held at
/// [most] items (the web's `:nth-child(n + 7)`).
Duration tkStagger(int index, {Duration each = Tk.tStagger, Duration start = Duration.zero, int most = 6}) =>
    start + each * math.min(index, most);

/// A part arriving once, when first shown: from faded, moved by [dx], [dy]
/// and scaled by [scale] to where it rests, over [duration] along [curve],
/// after [delay]. [fadeBy] is the share of the time the fade takes (the web's
/// keyframes that are opaque at 60% or 70%).
class TkIn extends StatefulWidget {
  const TkIn({
    super.key,
    required this.child,
    required this.duration,
    this.curve = Tk.ease,
    this.delay = Duration.zero,
    this.dx = 0,
    this.dy = 0,
    this.scale = 1,
    this.fade = true,
    this.fadeBy = 1,
    this.alignment = Alignment.center,
  });

  /// styles.css `rise-in` (0.36 s, 4 px): a list's rows, a page's stats and
  /// panels, [index] of them 30 ms apart.
  TkIn.rise({Key? key, required Widget child, int index = 0})
    : this(key: key, child: child, duration: const Duration(milliseconds: 360), dy: 4, delay: tkStagger(index, each: const Duration(milliseconds: 30), most: 5));

  /// styles.css `.notice, .error, .warning` (rise-in, 0.24 s).
  const TkIn.notice({Key? key, required Widget child})
    : this(key: key, child: child, duration: const Duration(milliseconds: 240), dy: 4);

  /// motion.css `.tk-stagger > *` (tk-spring-up): a list rising one after
  /// another, 40 ms apart.
  TkIn.stagger({Key? key, required Widget child, int index = 0, Duration start = Duration.zero})
    : this(
        key: key,
        child: child,
        duration: Tk.tPicker,
        curve: Tk.spring,
        dy: 14,
        scale: 0.98,
        fadeBy: 0.7,
        delay: tkStagger(index, start: start),
      );

  /// chat.css `.tk-sheet .tk-stagger > *` (tkd-up10, 0.3 s, from 120 ms,
  /// 40 ms apart): a sheet's rows following it up.
  TkIn.sheetRow({Key? key, required Widget child, int index = 0})
    : this(
        key: key,
        child: child,
        duration: const Duration(milliseconds: 300),
        curve: Curves.ease,
        dy: 10,
        delay: tkStagger(index, start: const Duration(milliseconds: 120), most: 5),
      );

  /// chat.css `.tk-tools-grid > *` (tkd-up8, 0.32 s, from 60 ms, 40 ms
  /// apart): the tools' tiles.
  TkIn.tile({Key? key, required Widget child, int index = 0})
    : this(
        key: key,
        child: child,
        duration: const Duration(milliseconds: 320),
        curve: Curves.ease,
        dy: 8,
        delay: tkStagger(index, start: const Duration(milliseconds: 60), most: 5),
      );

  /// motion.css `.toast` and `.tk-arrive` (tk-arrive): new things arriving
  /// with a little bounce.
  const TkIn.arrive({Key? key, required Widget child})
    : this(key: key, child: child, duration: Tk.tArrive, curve: Tk.springBounce, dy: 16, scale: 0.96, fadeBy: 0.6);

  /// chat.css `.tk-arrive` in a conversation (tkd-bounce): a card arriving.
  const TkIn.card({Key? key, required Widget child})
    : this(
        key: key,
        child: child,
        duration: Tk.tArrive,
        curve: const Cubic(0.2, 0.9, 0.25, 1.1),
        dy: 24,
        scale: 0.96,
        fadeBy: 0.6,
      );

  /// chat.css `.tk-brief, .tk-alerts > li` (tkd-card-rise, 0.5 s): the
  /// briefing and the alert cards, [index] of them 300 ms apart after 50 ms.
  TkIn.cardRise({Key? key, required Widget child, int index = 0})
    : this(
        key: key,
        child: child,
        duration: const Duration(milliseconds: 500),
        curve: const Cubic(0.2, 0.9, 0.25, 1.12),
        dy: 22,
        scale: 0.97,
        delay: tkStagger(index, each: const Duration(milliseconds: 300), start: const Duration(milliseconds: 50), most: 3),
      );

  /// styles.css `.empty-icon` (pop, 0.4 s).
  const TkIn.pop({Key? key, required Widget child})
    : this(key: key, child: child, duration: const Duration(milliseconds: 400), scale: 0.9);

  final Widget child;
  final Duration duration;
  final Curve curve;
  final Duration delay;
  final double dx;
  final double dy;
  final double scale;
  final bool fade;
  final double fadeBy;
  final Alignment alignment;

  @override
  State<TkIn> createState() => _TkInState();
}

class _TkInState extends State<TkIn> with SingleTickerProviderStateMixin {
  late final AnimationController _a = AnimationController(vsync: this, duration: widget.delay + widget.duration);
  late final double _from = widget.delay.inMicroseconds / (widget.delay + widget.duration).inMicroseconds;
  late final _move = CurvedAnimation(parent: _a, curve: Interval(_from, 1, curve: widget.curve));
  late final _fade = CurvedAnimation(parent: _a, curve: Interval(_from, _from + (1 - _from) * widget.fadeBy, curve: Curves.linear));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (tkStill(context)) {
      _a.value = 1;
    } else if (_a.value == 0 && !_a.isAnimating) {
      _a.forward();
    }
  }

  @override
  void dispose() {
    _move.dispose();
    _fade.dispose();
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _a,
    // The same parts at rest as moving: a part whose parents changed would
    // be made afresh, losing what it holds.
    builder: (context, child) {
      final m = _a.isCompleted ? 1.0 : _move.value;
      return Opacity(
        alwaysIncludeSemantics: true,
        opacity: !widget.fade || _a.isCompleted ? 1 : _fade.value.clamp(0.0, 1.0),
        child: Transform(
          alignment: widget.alignment,
          transform: Matrix4.translationValues(widget.dx * (1 - m), widget.dy * (1 - m), 0)
            ..scaleByDouble(1 + (widget.scale - 1) * (1 - m), 1 + (widget.scale - 1) * (1 - m), 1, 1),
          child: child,
        ),
      );
    },
    child: widget.child,
  );
}

/// A bar or a meter growing from nothing (styles.css `grow-x` 0.8 s on a
/// progress or meter; chat.css `tkd-grow-bar` 0.6 s on the briefing's bars,
/// [index] of them 50 ms apart from 300 ms).
class TkGrow extends StatefulWidget {
  const TkGrow({super.key, required this.child, this.vertical = false, this.duration = const Duration(milliseconds: 800), this.curve = Tk.ease, this.delay = Duration.zero});

  TkGrow.bar({Key? key, required Widget child, int index = 0})
    : this(
        key: key,
        child: child,
        vertical: true,
        duration: const Duration(milliseconds: 600),
        curve: const Cubic(0.2, 0.9, 0.25, 1.1),
        delay: tkStagger(index, each: const Duration(milliseconds: 50), start: const Duration(milliseconds: 300)),
      );

  final Widget child;
  final bool vertical;
  final Duration duration;
  final Curve curve;
  final Duration delay;

  @override
  State<TkGrow> createState() => _TkGrowState();
}

class _TkGrowState extends State<TkGrow> with SingleTickerProviderStateMixin {
  late final _a = AnimationController(vsync: this, duration: widget.delay + widget.duration);
  late final _c = CurvedAnimation(
    parent: _a,
    curve: Interval(widget.delay.inMicroseconds / (widget.delay + widget.duration).inMicroseconds, 1, curve: widget.curve),
  );

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (tkStill(context)) {
      _a.value = 1;
    } else if (_a.value == 0 && !_a.isAnimating) {
      _a.forward();
    }
  }

  @override
  void dispose() {
    _c.dispose();
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _c,
    builder: (context, child) => Transform(
      alignment: widget.vertical ? Alignment.bottomCenter : Alignment.centerLeft,
      transform: Matrix4.diagonal3Values(widget.vertical ? 1 : math.max(0, _c.value), widget.vertical ? math.max(0, _c.value) : 1, 1),
      child: child,
    ),
    child: widget.child,
  );
}

/// A ring pulsing out from a dot or a pill, for ever (styles.css `live`,
/// 2.4 s; chat.css `tkd-pulse-lime` 2 s after 1.2 s on the briefing's
/// live dot): [color] at its start, gone [spread] px out at the middle.
class TkPulse extends StatefulWidget {
  const TkPulse({super.key, required this.child, required this.color, this.spread = 4, this.period = const Duration(milliseconds: 2400), this.delay = Duration.zero, this.radius = 999});
  final Widget child;
  final Color color;
  final double spread;
  final Duration period;
  final Duration delay;
  final double radius;

  @override
  State<TkPulse> createState() => _TkPulseState();
}

class _TkPulseState extends State<TkPulse> with SingleTickerProviderStateMixin {
  late final _a = AnimationController(vsync: this, duration: widget.period);
  bool _started = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final run = tkRepeatingMotion && !tkStill(context);
    if (!run) {
      _a.stop();
      _a.value = 0;
    } else if (!_started) {
      _started = true;
      Future<void>.delayed(widget.delay, () {
        if (mounted && tkRepeatingMotion && !tkStill(context)) _a.repeat();
      });
    }
  }

  @override
  void dispose() {
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _a,
    builder: (context, child) {
      // 0 → 50% → 100%: from the colour at no spread, out and gone, back.
      final p = _a.value < 0.5 ? _a.value * 2 : 2 - _a.value * 2;
      final ease = Curves.easeInOut.transform(p);
      return DecoratedBox(
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(widget.radius),
          boxShadow: _a.isAnimating
              ? [BoxShadow(color: widget.color.withValues(alpha: widget.color.a * (1 - ease)), spreadRadius: widget.spread * ease)]
              : null,
        ),
        child: child,
      );
    },
    child: widget.child,
  );
}

/// kit.tsx's fresh diary line (chat.css `tkd-land`, 1.8 s): it drops in
/// from 14 px above lit in lime, and over the rest of the time the lime
/// goes to its own [decoration]'s colour. Not fresh, it is simply drawn.
class TkLand extends StatefulWidget {
  const TkLand({super.key, required this.child, required this.decoration, this.fresh = true});
  final Widget child;
  final BoxDecoration decoration;

  /// Only a line just added lands; one already there is simply shown.
  final bool fresh;

  @override
  State<TkLand> createState() => _TkLandState();
}

class _TkLandState extends State<TkLand> with SingleTickerProviderStateMixin {
  static const _lime = Color(0xFFC8F169);
  late final _a = AnimationController(vsync: this, duration: const Duration(milliseconds: 1800));
  late final _drop = CurvedAnimation(parent: _a, curve: const Interval(0, 0.4, curve: Cubic(0.2, 0.9, 0.25, 1.05)));
  late final _glow = CurvedAnimation(parent: _a, curve: const Interval(0.4, 1, curve: Curves.easeOut));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!widget.fresh || tkStill(context)) {
      _a.value = 1;
    } else if (_a.value == 0 && !_a.isAnimating) {
      _a.forward();
    }
  }

  @override
  void dispose() {
    _drop.dispose();
    _glow.dispose();
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _a,
    builder: (context, child) {
      final box = DecoratedBox(
        decoration: _a.isCompleted ? widget.decoration : widget.decoration.copyWith(color: Color.lerp(_lime, widget.decoration.color, _glow.value)),
        child: child,
      );
      final d = _a.isCompleted ? 1.0 : _drop.value;
      return Opacity(
        alwaysIncludeSemantics: true,
        opacity: d.clamp(0.0, 1.0),
        child: Transform(
          alignment: Alignment.center,
          transform: Matrix4.translationValues(0, -14 * (1 - d), 0)..scaleByDouble(0.96 + 0.04 * d, 0.96 + 0.04 * d, 1, 1),
          child: box,
        ),
      );
    },
    child: widget.child,
  );
}

/// A number or a line rolling up into place (chat.css `tkd-roll`, 0.45 s):
/// when shown, and again whenever [value] changes.
class TkRoll extends StatefulWidget {
  const TkRoll({super.key, required this.value, required this.child, this.onShow = true});
  final Object value;
  final Widget child;

  /// Whether it rolls in when first shown (a fresh line), or only on a change.
  final bool onShow;

  @override
  State<TkRoll> createState() => _TkRollState();
}

class _TkRollState extends State<TkRoll> with SingleTickerProviderStateMixin {
  late final _a = AnimationController(vsync: this, duration: Tk.tRoll, value: widget.onShow ? 0 : 1);
  late final _c = CurvedAnimation(parent: _a, curve: const Cubic(0.2, 0.9, 0.25, 1.1));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (tkStill(context)) {
      _a.value = 1;
    } else if (_a.value == 0 && !_a.isAnimating) {
      _a.forward();
    }
  }

  @override
  void didUpdateWidget(TkRoll old) {
    super.didUpdateWidget(old);
    if (old.value != widget.value && !tkStill(context)) _a.forward(from: 0);
  }

  @override
  void dispose() {
    _c.dispose();
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _c,
    builder: (context, child) => Opacity(
      alwaysIncludeSemantics: true,
      opacity: _a.value,
      child: FractionalTranslation(translation: Offset(0, _a.isCompleted ? 0 : 1 - _c.value), child: child),
    ),
    child: widget.child,
  );
}

/// How a message comes into an open conversation (chat.css): a bubble
/// slides in from its own side, a card arrives with a little bounce, a
/// diary line lands lit in lime (its own [TkLand]).
enum TkArrival { bubble, card, diary }

/// Whether what is under it has just arrived in an open conversation (a
/// diary line lands, its new value rolls); what was there already is not.
class TkFresh extends InheritedWidget {
  const TkFresh({super.key, required this.fresh, required super.child});
  final bool fresh;

  static bool of(BuildContext context) => context.dependOnInheritedWidgetOfExactType<TkFresh>()?.fresh ?? false;

  @override
  bool updateShouldNotify(TkFresh old) => old.fresh != fresh;
}
