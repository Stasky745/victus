// Drag-and-drop reordering for the Meal Categories page (/meals/categories).
// Only loaded on that one page, not the global layout — no other page has a
// sortable list.
document.addEventListener("DOMContentLoaded", function () {
	var list = document.getElementById("category-list");
	if (!list) return;

	var csrfToken = document.querySelector('input[name="gorilla.csrf.Token"]').value;

	Sortable.create(list, {
		handle: ".drag-handle",
		animation: 150,
		onEnd: function () {
			var ids = Array.from(list.children).map(function (li) {
				return li.dataset.categoryId;
			});
			var body = new URLSearchParams();
			ids.forEach(function (id) {
				body.append("order_ids", id);
			});
			fetch("/meals/categories/reorder", {
				method: "POST",
				headers: {
					"X-CSRF-Token": csrfToken,
					"Content-Type": "application/x-www-form-urlencoded",
				},
				body: body,
			}).then(function (res) {
				// Out of sync with the server — reload to the real order
				// rather than leaving the UI showing an order that didn't
				// actually save.
				if (!res.ok) location.reload();
			}).catch(function () {
				location.reload();
			});
		},
	});
});
