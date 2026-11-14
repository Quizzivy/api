package domain

import (
	"slices"
	"strings"
)

func validateMixedOutline(in UpdateInput, errs *ValidationError) {
	if in.GroupOutline && !in.SetSections {
		errs.AddIn("sections", "Cần gửi toàn bộ cấu trúc đề khi lưu thứ tự nhóm.", "Send the whole structure of the test when saving the order of its groups.")
	}
	groups := map[string]bool{}
	sections := map[string]bool{}
	for i, section := range in.Sections {
		sectionID := strings.ToLower(section.ID)
		if sectionID != "" && sections[sectionID] {
			errs.AddIn(sectionField(i, "id"), "Một phần chỉ được xuất hiện một lần trong đề.", "A section can appear only once in a test.")
		}
		sections[sectionID] = true
		if !in.GroupOutline {
			if section.SetUnits {
				errs.AddIn(sectionField(i, "units"), "Cần định dạng cấu trúc nhóm để lưu ngữ liệu chung.", "Saving shared material needs the structure format that has groups.")
			}
			continue
		}
		if !section.SetUnits {
			errs.AddIn(sectionField(i, "units"), "Cần gửi đủ thứ tự câu hỏi và nhóm của phần.", "Send the whole order of the section's questions and groups.")
			continue
		}
		validateSectionUnits(i, section, groups, errs)
	}
}

func validateSectionUnits(index int, section SectionInput, groups map[string]bool, errs *ValidationError) {
	field := sectionField(index, "units")
	questions := []string{}
	if len(section.Units) > 32768 {
		errs.AddIn(field, "Phần vượt giới hạn số mục trong cấu trúc.", "The section has more items than a structure can hold.")
	}
	for _, unit := range section.Units {
		id := strings.ToLower(unit.ID)
		switch unit.Kind {
		case "question":
			questions = append(questions, id)
		case "group":
			if groups[id] {
				errs.AddIn(field, "Một nhóm ngữ liệu chỉ được xuất hiện một lần trong đề.", "A group with shared material can appear only once in a test.")
			}
			groups[id] = true
		default:
			errs.AddIn(field, "Loại mục trong cấu trúc không hợp lệ.", "The kind of an item in the structure is not valid.")
		}
	}
	projection := make([]string, len(section.QuestionIDs))
	for i, id := range section.QuestionIDs {
		projection[i] = strings.ToLower(id)
	}
	if !slices.Equal(questions, projection) {
		errs.AddIn(field, "Thứ tự câu hỏi không khớp cấu trúc đã gửi.", "The order of the questions does not match the structure that was sent.")
	}
}
