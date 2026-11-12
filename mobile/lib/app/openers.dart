// Screens the app's shell opens, for screens deep in a tool that lead to
// them (an empty list's "Linked businesses", a draft's "Find them"): set
// by the signed-in home, null in a test or before it.

import 'package:flutter/widgets.dart';

class Openers {
  /// Linked businesses (the web's /chat links page).
  static void Function(BuildContext context)? linkedBusinesses;

  /// A linked business's shop in the order sheet (a supplier's Prices):
  /// to order from with leave to, else to look around.
  static void Function(BuildContext context, String link, String business)? orderFrom;

  /// Find a business (Explore).
  static void Function(BuildContext context)? findBusiness;
}
